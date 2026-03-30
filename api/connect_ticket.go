package main

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"path"
	"strings"
	"sync"
	"time"

	"github.com/labstack/echo/v4"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/util/retry"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

const (
	connectTicketTypeACPConversation = "acp-conversation"
	connectTicketTypeTerminal        = "terminal"

	connectTicketACPProtocol      = "spritz-acp.v1"
	connectTicketTerminalProtocol = "spritz-terminal.v1"
	connectTicketProtocolPrefix   = "spritz-ticket.v1."

	connectTicketLabelKey   = "spritz.sh/connect-ticket"
	connectTicketLabelValue = "true"

	connectTicketHashKey       = "hash"
	connectTicketTypeKey       = "type"
	connectTicketProtocolKey   = "protocol"
	connectTicketPathKey       = "connectPath"
	connectTicketOriginKey     = "origin"
	connectTicketExpiresAtKey  = "expiresAt"
	connectTicketUsedKey       = "used"
	connectTicketUsedAtKey     = "usedAt"
	connectTicketPrincipalKey  = "principal"
	defaultConnectTicketTTL    = 45 * time.Second
	defaultConnectTicketGC     = 5 * time.Minute
	connectTicketRandomBytes   = 32
	connectTicketNameHashChars = 40
)

var (
	errInvalidConnectTicket = errors.New("invalid connect ticket")
	errExpiredConnectTicket = errors.New("expired connect ticket")
	errUsedConnectTicket    = errors.New("used connect ticket")
)

type connectTicketRecord struct {
	Type        string
	Hash        string
	Protocol    string
	ConnectPath string
	Origin      string
	Principal   principal
	ExpiresAt   time.Time
	Used        bool
	UsedAt      time.Time
}

type connectTicketStore struct {
	client          client.Client
	namespace       string
	ttl             time.Duration
	cleanupInterval time.Duration
	mu              sync.Mutex
	lastCleanup     time.Time
}

type connectTicketResponse struct {
	Type        string `json:"type"`
	Ticket      string `json:"ticket"`
	ExpiresAt   string `json:"expiresAt"`
	Protocol    string `json:"protocol"`
	ConnectPath string `json:"connectPath"`
}

type terminalConnectTicketRequest struct {
	Session string `json:"session,omitempty"`
}

func newConnectTicketStore(client client.Client, namespace string) *connectTicketStore {
	ttl := parseDurationEnv("SPRITZ_CONNECT_TICKET_TTL", defaultConnectTicketTTL)
	if ttl <= 0 {
		ttl = defaultConnectTicketTTL
	}
	cleanupInterval := parseDurationEnv("SPRITZ_CONNECT_TICKET_CLEANUP_INTERVAL", defaultConnectTicketGC)
	if cleanupInterval <= 0 {
		cleanupInterval = defaultConnectTicketGC
	}
	return &connectTicketStore{
		client:          client,
		namespace:       namespace,
		ttl:             ttl,
		cleanupInterval: cleanupInterval,
		lastCleanup:     time.Now(),
	}
}

func (s *connectTicketStore) issue(ctx context.Context, record connectTicketRecord) (string, connectTicketRecord, error) {
	if s == nil {
		return "", connectTicketRecord{}, errors.New("connect ticket store is not configured")
	}
	if err := s.cleanupExpired(ctx); err != nil {
		return "", connectTicketRecord{}, err
	}

	for attempt := 0; attempt < 3; attempt++ {
		token, hash, err := newOpaqueConnectTicket()
		if err != nil {
			return "", connectTicketRecord{}, err
		}
		record.Hash = hash
		if record.ExpiresAt.IsZero() {
			record.ExpiresAt = time.Now().UTC().Add(s.ttl)
		}
		configMap, err := connectTicketConfigMap(s.namespace, record)
		if err != nil {
			return "", connectTicketRecord{}, err
		}
		if err := s.client.Create(ctx, configMap); err != nil {
			if apierrors.IsAlreadyExists(err) {
				continue
			}
			return "", connectTicketRecord{}, err
		}
		return token, record, nil
	}
	return "", connectTicketRecord{}, errors.New("failed to allocate connect ticket")
}

func (s *connectTicketStore) consume(
	ctx context.Context,
	token string,
	validate func(connectTicketRecord) error,
) (connectTicketRecord, error) {
	if s == nil {
		return connectTicketRecord{}, errors.New("connect ticket store is not configured")
	}
	token = strings.TrimSpace(token)
	if token == "" {
		return connectTicketRecord{}, errInvalidConnectTicket
	}

	hash := sha256Hex(token)
	name := connectTicketName(hash)
	var consumed connectTicketRecord

	err := retry.RetryOnConflict(retry.DefaultRetry, func() error {
		current := &corev1.ConfigMap{}
		if err := s.client.Get(ctx, clientKey(s.namespace, name), current); err != nil {
			if apierrors.IsNotFound(err) {
				return errInvalidConnectTicket
			}
			return err
		}
		record, err := connectTicketRecordFromConfigMap(current)
		if err != nil {
			return err
		}
		if record.Hash != hash {
			return errInvalidConnectTicket
		}
		if record.Used {
			return errUsedConnectTicket
		}
		if !record.ExpiresAt.IsZero() && time.Now().UTC().After(record.ExpiresAt) {
			return errExpiredConnectTicket
		}
		if validate != nil {
			if err := validate(record); err != nil {
				return err
			}
		}

		record.Used = true
		record.UsedAt = time.Now().UTC()
		if err := writeConnectTicketRecordToConfigMap(current, record); err != nil {
			return err
		}
		if err := s.client.Update(ctx, current); err != nil {
			return err
		}
		consumed = record
		return nil
	})
	if err != nil {
		return connectTicketRecord{}, err
	}

	_ = s.client.Delete(ctx, &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: s.namespace},
	})
	return consumed, nil
}

func (s *connectTicketStore) cleanupExpired(ctx context.Context) error {
	if s == nil || s.cleanupInterval <= 0 {
		return nil
	}
	now := time.Now()

	s.mu.Lock()
	if now.Sub(s.lastCleanup) < s.cleanupInterval {
		s.mu.Unlock()
		return nil
	}
	s.lastCleanup = now
	s.mu.Unlock()

	list := &corev1.ConfigMapList{}
	if err := s.client.List(ctx, list, client.InNamespace(s.namespace), client.MatchingLabels{
		connectTicketLabelKey: connectTicketLabelValue,
	}); err != nil {
		return err
	}

	for _, item := range list.Items {
		record, err := connectTicketRecordFromConfigMap(&item)
		if err != nil {
			continue
		}
		if record.Used || (!record.ExpiresAt.IsZero() && now.UTC().After(record.ExpiresAt)) {
			configMap := item.DeepCopy()
			_ = s.client.Delete(ctx, configMap)
		}
	}
	return nil
}

func newOpaqueConnectTicket() (string, string, error) {
	raw := make([]byte, connectTicketRandomBytes)
	if _, err := rand.Read(raw); err != nil {
		return "", "", err
	}
	token := base64.RawURLEncoding.EncodeToString(raw)
	return token, sha256Hex(token), nil
}

func sha256Hex(value string) string {
	sum := sha256.Sum256([]byte(strings.TrimSpace(value)))
	return fmt.Sprintf("%x", sum[:])
}

func connectTicketName(hash string) string {
	trimmed := strings.TrimSpace(hash)
	if len(trimmed) > connectTicketNameHashChars {
		trimmed = trimmed[:connectTicketNameHashChars]
	}
	return "spritz-connect-ticket-" + trimmed
}

func connectTicketConfigMap(namespace string, record connectTicketRecord) (*corev1.ConfigMap, error) {
	configMap := &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{
			Name:      connectTicketName(record.Hash),
			Namespace: namespace,
			Labels: map[string]string{
				connectTicketLabelKey: connectTicketLabelValue,
			},
		},
	}
	if err := writeConnectTicketRecordToConfigMap(configMap, record); err != nil {
		return nil, err
	}
	return configMap, nil
}

func connectTicketRecordFromConfigMap(configMap *corev1.ConfigMap) (connectTicketRecord, error) {
	if configMap == nil {
		return connectTicketRecord{}, nil
	}
	record := connectTicketRecord{
		Hash:        strings.TrimSpace(configMap.Data[connectTicketHashKey]),
		Type:        strings.TrimSpace(configMap.Data[connectTicketTypeKey]),
		Protocol:    strings.TrimSpace(configMap.Data[connectTicketProtocolKey]),
		ConnectPath: strings.TrimSpace(configMap.Data[connectTicketPathKey]),
		Origin:      strings.TrimSpace(configMap.Data[connectTicketOriginKey]),
		Used:        strings.EqualFold(strings.TrimSpace(configMap.Data[connectTicketUsedKey]), "true"),
	}
	if value := strings.TrimSpace(configMap.Data[connectTicketExpiresAtKey]); value != "" {
		parsed, err := time.Parse(time.RFC3339Nano, value)
		if err != nil {
			return connectTicketRecord{}, err
		}
		record.ExpiresAt = parsed.UTC()
	}
	if value := strings.TrimSpace(configMap.Data[connectTicketUsedAtKey]); value != "" {
		parsed, err := time.Parse(time.RFC3339Nano, value)
		if err != nil {
			return connectTicketRecord{}, err
		}
		record.UsedAt = parsed.UTC()
	}
	if value := strings.TrimSpace(configMap.Data[connectTicketPrincipalKey]); value != "" {
		if err := json.Unmarshal([]byte(value), &record.Principal); err != nil {
			return connectTicketRecord{}, err
		}
	}
	return record, nil
}

func writeConnectTicketRecordToConfigMap(configMap *corev1.ConfigMap, record connectTicketRecord) error {
	if configMap.Data == nil {
		configMap.Data = map[string]string{}
	}
	principalJSON, err := json.Marshal(record.Principal)
	if err != nil {
		return err
	}
	configMap.Data[connectTicketHashKey] = strings.TrimSpace(record.Hash)
	configMap.Data[connectTicketTypeKey] = strings.TrimSpace(record.Type)
	configMap.Data[connectTicketProtocolKey] = strings.TrimSpace(record.Protocol)
	configMap.Data[connectTicketPathKey] = strings.TrimSpace(record.ConnectPath)
	configMap.Data[connectTicketOriginKey] = strings.TrimSpace(record.Origin)
	configMap.Data[connectTicketExpiresAtKey] = record.ExpiresAt.UTC().Format(time.RFC3339Nano)
	configMap.Data[connectTicketPrincipalKey] = string(principalJSON)
	if record.Used {
		configMap.Data[connectTicketUsedKey] = "true"
	} else {
		configMap.Data[connectTicketUsedKey] = "false"
	}
	if !record.UsedAt.IsZero() {
		configMap.Data[connectTicketUsedAtKey] = record.UsedAt.UTC().Format(time.RFC3339Nano)
	} else {
		delete(configMap.Data, connectTicketUsedAtKey)
	}
	return nil
}

func requestPrincipal(c echo.Context, auth authConfig) (principal, error) {
	if value, ok := principalFromContext(c); ok {
		return value, nil
	}
	return auth.principal(c.Request())
}

func requestedWebSocketProtocols(r *http.Request) []string {
	raw := strings.TrimSpace(r.Header.Get("Sec-WebSocket-Protocol"))
	if raw == "" {
		return nil
	}
	items := strings.Split(raw, ",")
	protocols := make([]string, 0, len(items))
	for _, item := range items {
		trimmed := strings.TrimSpace(item)
		if trimmed != "" {
			protocols = append(protocols, trimmed)
		}
	}
	return protocols
}

func requestedApplicationProtocol(r *http.Request, expected string) []string {
	for _, protocol := range requestedWebSocketProtocols(r) {
		if protocol == expected {
			return []string{expected}
		}
	}
	return nil
}

func requestedConnectTicket(r *http.Request) string {
	for _, protocol := range requestedWebSocketProtocols(r) {
		if strings.HasPrefix(protocol, connectTicketProtocolPrefix) {
			return strings.TrimPrefix(protocol, connectTicketProtocolPrefix)
		}
	}
	return ""
}

func canonicalConnectPath(r *http.Request) string {
	if r == nil || r.URL == nil {
		return ""
	}
	encoded := r.URL.Query().Encode()
	if encoded == "" {
		return r.URL.Path
	}
	return r.URL.Path + "?" + encoded
}

func requestOrigin(r *http.Request) string {
	if r == nil {
		return ""
	}
	return strings.TrimSpace(r.Header.Get("Origin"))
}

func (s *server) issueConnectTicket(ctx context.Context, record connectTicketRecord) (connectTicketResponse, error) {
	token, stored, err := s.connectTickets.issue(ctx, record)
	if err != nil {
		return connectTicketResponse{}, err
	}
	return connectTicketResponse{
		Type:        "connect-ticket",
		Ticket:      token,
		ExpiresAt:   stored.ExpiresAt.UTC().Format(time.RFC3339Nano),
		Protocol:    stored.Protocol,
		ConnectPath: stored.ConnectPath,
	}, nil
}

func (s *server) authenticateWebSocketRequest(
	c echo.Context,
	expectedType string,
	expectedProtocol string,
) (principal, []string, error) {
	resolvedPrincipal, err := s.auth.principal(c.Request())
	if err == nil {
		return resolvedPrincipal, requestedApplicationProtocol(c.Request(), expectedProtocol), nil
	}
	if !errors.Is(err, errUnauthenticated) {
		return principal{}, nil, err
	}

	ticket := requestedConnectTicket(c.Request())
	if strings.TrimSpace(ticket) == "" {
		return principal{}, nil, errUnauthenticated
	}
	record, err := s.connectTickets.consume(c.Request().Context(), ticket, func(record connectTicketRecord) error {
		if record.Type != expectedType {
			return errInvalidConnectTicket
		}
		if record.Protocol != expectedProtocol {
			return errInvalidConnectTicket
		}
		if record.ConnectPath != canonicalConnectPath(c.Request()) {
			return errInvalidConnectTicket
		}
		if origin := strings.TrimSpace(record.Origin); origin != "" && !strings.EqualFold(origin, requestOrigin(c.Request())) {
			return errInvalidConnectTicket
		}
		if len(requestedApplicationProtocol(c.Request(), expectedProtocol)) == 0 {
			return errInvalidConnectTicket
		}
		return nil
	})
	if err != nil {
		if errors.Is(err, errInvalidConnectTicket) || errors.Is(err, errExpiredConnectTicket) || errors.Is(err, errUsedConnectTicket) {
			return principal{}, nil, errUnauthenticated
		}
		return principal{}, nil, err
	}
	return record.Principal, []string{expectedProtocol}, nil
}

func (s *server) createACPConnectTicket(c echo.Context) error {
	if !s.acp.enabled {
		return writeError(c, http.StatusNotFound, "acp disabled")
	}
	principal, err := requestPrincipal(c, s.auth)
	if err != nil {
		return writeAuthError(c, err)
	}
	if err := ensureAuthenticated(principal, s.auth.enabled()); err != nil {
		return writeAuthError(c, err)
	}

	namespace := s.requestNamespace(c)
	conversation, err := s.getAuthorizedACPConversation(c.Request().Context(), principal, namespace, c.Param("id"))
	if err != nil {
		return s.writeACPConversationError(c, err)
	}
	if _, err := s.getAuthorizedACPReadySpritzForConversation(c.Request().Context(), conversation, namespace); err != nil {
		return s.writeACPResourceError(c, err)
	}

	response, err := s.issueConnectTicket(c.Request().Context(), connectTicketRecord{
		Type:        connectTicketTypeACPConversation,
		Protocol:    connectTicketACPProtocol,
		ConnectPath: s.acpConnectPath(c, conversation.Name),
		Origin:      requestOrigin(c.Request()),
		Principal:   principal,
	})
	if err != nil {
		return writeError(c, http.StatusInternalServerError, err.Error())
	}
	return writeJSON(c, http.StatusOK, response)
}

func (s *server) createTerminalConnectTicket(c echo.Context) error {
	if !s.terminal.enabled {
		return writeError(c, http.StatusNotFound, "terminal disabled")
	}
	principal, err := requestPrincipal(c, s.auth)
	if err != nil {
		return writeAuthError(c, err)
	}
	if err := ensureAuthenticated(principal, s.auth.enabled()); err != nil {
		return writeAuthError(c, err)
	}

	name := strings.TrimSpace(c.Param("name"))
	if name == "" {
		return writeError(c, http.StatusBadRequest, "spritz name required")
	}

	var body terminalConnectTicketRequest
	if err := decodeACPBody(c, &body); err != nil {
		return writeError(c, http.StatusBadRequest, err.Error())
	}
	body.Session = strings.TrimSpace(body.Session)

	namespace := s.requestNamespace(c)
	if namespace == "" {
		namespace = "default"
	}
	spritz, err := s.getAuthorizedSpritz(c.Request().Context(), principal, namespace, name)
	if err != nil {
		if apierrors.IsNotFound(err) {
			return writeError(c, http.StatusNotFound, "spritz not found")
		}
		if errors.Is(err, errForbidden) {
			return writeForbidden(c)
		}
		return writeError(c, http.StatusInternalServerError, err.Error())
	}

	response, err := s.issueConnectTicket(c.Request().Context(), connectTicketRecord{
		Type:        connectTicketTypeTerminal,
		Protocol:    connectTicketTerminalProtocol,
		ConnectPath: s.terminalConnectPath(c, spritz.Name, body.Session),
		Origin:      requestOrigin(c.Request()),
		Principal:   principal,
	})
	if err != nil {
		return writeError(c, http.StatusInternalServerError, err.Error())
	}
	return writeJSON(c, http.StatusOK, response)
}

func (s *server) connectPathWithQuery(c echo.Context, relativePath string, query url.Values) string {
	values := url.Values{}
	if query != nil {
		for key, items := range query {
			for _, item := range items {
				values.Add(key, item)
			}
		}
	}
	if strings.TrimSpace(s.namespace) == "" {
		if namespace := strings.TrimSpace(c.QueryParam("namespace")); namespace != "" {
			values.Set("namespace", namespace)
		}
	}
	fullPath := path.Join(s.apiPathPrefix(), relativePath)
	if !strings.HasPrefix(fullPath, "/") {
		fullPath = "/" + fullPath
	}
	if encoded := values.Encode(); encoded != "" {
		return fullPath + "?" + encoded
	}
	return fullPath
}

func (s *server) acpConnectPath(c echo.Context, conversationID string) string {
	return s.connectPathWithQuery(c, "/acp/conversations/"+url.PathEscape(strings.TrimSpace(conversationID))+"/connect", nil)
}

func (s *server) terminalConnectPath(c echo.Context, name, session string) string {
	query := url.Values{}
	if trimmed := strings.TrimSpace(session); trimmed != "" {
		query.Set("session", trimmed)
	}
	return s.connectPathWithQuery(c, "/spritzes/"+url.PathEscape(strings.TrimSpace(name))+"/terminal", query)
}
