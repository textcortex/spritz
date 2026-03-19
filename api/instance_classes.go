package main

import (
	"encoding/json"
	"fmt"
	"os"
	"sort"
	"strings"
)

const (
	instanceClassesEnvKey                   = "SPRITZ_INSTANCE_CLASSES_JSON"
	instanceClassAnnotationKey              = "spritz.sh/instance-class"
	instanceClassVersionAnnotationKey       = "spritz.sh/instance-class-version"
	requiredResolvedFieldServiceAccountName = "serviceAccountName"
)

type instanceClass struct {
	ID          string                        `json:"id,omitempty"`
	Version     string                        `json:"version,omitempty"`
	Description string                        `json:"description,omitempty"`
	Creation    instanceClassCreationPolicy   `json:"creation,omitempty"`
	Access      json.RawMessage               `json:"access,omitempty"`
	Session     json.RawMessage               `json:"session,omitempty"`
	Delegation  instanceClassDelegationPolicy `json:"delegation,omitempty"`
	Credentials json.RawMessage               `json:"credentials,omitempty"`
	Lifecycle   json.RawMessage               `json:"lifecycle,omitempty"`
	Audit       json.RawMessage               `json:"audit,omitempty"`
}

type instanceClassCreationPolicy struct {
	RequireOwner           bool     `json:"requireOwner,omitempty"`
	RequiredResolvedFields []string `json:"requiredResolvedFields,omitempty"`
}

type instanceClassDelegationPolicy struct {
	Intents map[string]delegatedServiceIntentPolicy `json:"intents,omitempty"`
}

type delegatedServiceIntentPolicy struct {
	Authn                 string   `json:"authn,omitempty"`
	ChargedPrincipal      string   `json:"chargedPrincipal,omitempty"`
	ActorCheck            string   `json:"actorCheck,omitempty"`
	AllowedServiceClasses []string `json:"allowedServiceClasses,omitempty"`
}

type instanceClassCatalog struct {
	byID map[string]instanceClass
}

func newInstanceClassCatalog() (instanceClassCatalog, error) {
	raw := strings.TrimSpace(os.Getenv(instanceClassesEnvKey))
	if raw == "" {
		return instanceClassCatalog{}, nil
	}
	var items []instanceClass
	if err := json.Unmarshal([]byte(raw), &items); err != nil {
		return instanceClassCatalog{}, fmt.Errorf("invalid %s: %w", instanceClassesEnvKey, err)
	}
	if len(items) == 0 {
		return instanceClassCatalog{}, fmt.Errorf("invalid %s: at least one instance class is required", instanceClassesEnvKey)
	}
	out := instanceClassCatalog{byID: map[string]instanceClass{}}
	for index, item := range items {
		id := sanitizeSpritzNameToken(item.ID)
		if id == "" {
			return instanceClassCatalog{}, fmt.Errorf("invalid %s: classes[%d].id is required", instanceClassesEnvKey, index)
		}
		if _, exists := out.byID[id]; exists {
			return instanceClassCatalog{}, fmt.Errorf("invalid %s: duplicate instance class id %q", instanceClassesEnvKey, id)
		}
		item.ID = id
		item.Version = strings.TrimSpace(item.Version)
		item.Description = strings.TrimSpace(item.Description)
		normalizedFields := make([]string, 0, len(item.Creation.RequiredResolvedFields))
		seenFields := map[string]struct{}{}
		for _, field := range item.Creation.RequiredResolvedFields {
			normalized := normalizeRequiredResolvedField(field)
			if normalized == "" {
				return instanceClassCatalog{}, fmt.Errorf("invalid %s: classes[%d].creation.requiredResolvedFields contains an unsupported field %q", instanceClassesEnvKey, index, field)
			}
			if _, exists := seenFields[normalized]; exists {
				continue
			}
			seenFields[normalized] = struct{}{}
			normalizedFields = append(normalizedFields, normalized)
		}
		sort.Strings(normalizedFields)
		item.Creation.RequiredResolvedFields = normalizedFields
		out.byID[id] = item
	}
	return out, nil
}

func normalizeRequiredResolvedField(raw string) string {
	switch strings.TrimSpace(raw) {
	case requiredResolvedFieldServiceAccountName:
		return requiredResolvedFieldServiceAccountName
	default:
		return ""
	}
}

func (c instanceClassCatalog) get(id string) (*instanceClass, bool) {
	id = sanitizeSpritzNameToken(id)
	if id == "" || len(c.byID) == 0 {
		return nil, false
	}
	item, ok := c.byID[id]
	if !ok {
		return nil, false
	}
	copy := item
	return &copy, true
}

func (c instanceClassCatalog) validatePresetCatalog(presets presetCatalog) error {
	for _, preset := range presets.byID {
		if strings.TrimSpace(preset.InstanceClass) == "" {
			continue
		}
		if _, ok := c.get(preset.InstanceClass); ok {
			continue
		}
		return fmt.Errorf("preset %q references unknown instance class %q", preset.ID, preset.InstanceClass)
	}
	return nil
}

func (c instanceClass) validateResolvedCreate(body *createRequest) error {
	if body == nil {
		return nil
	}
	if c.Creation.RequireOwner && strings.TrimSpace(body.Spec.Owner.ID) == "" {
		return fmt.Errorf("instance class %q requires an owner", c.ID)
	}
	for _, field := range c.Creation.RequiredResolvedFields {
		switch field {
		case requiredResolvedFieldServiceAccountName:
			if strings.TrimSpace(body.Spec.ServiceAccountName) == "" {
				return fmt.Errorf("instance class %q requires resolved field %q", c.ID, field)
			}
		default:
			return fmt.Errorf("instance class %q references unsupported resolved field %q", c.ID, field)
		}
	}
	return nil
}

func (c instanceClass) requiresResolvedField(field string) bool {
	for _, candidate := range c.Creation.RequiredResolvedFields {
		if candidate == field {
			return true
		}
	}
	return false
}
