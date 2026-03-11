package main

import (
	"context"
	"strings"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/util/retry"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

type idempotencyReservationRecord struct {
	fingerprint string
	name        string
	payload     string
	completed   bool
}

type idempotencyReservationStore struct {
	client    client.Client
	namespace string
}

func newIdempotencyReservationStore(client client.Client, namespace string) *idempotencyReservationStore {
	return &idempotencyReservationStore{
		client:    client,
		namespace: namespace,
	}
}

func (s *idempotencyReservationStore) get(ctx context.Context, actorID, key string) (idempotencyReservationRecord, bool, error) {
	if strings.TrimSpace(actorID) == "" || strings.TrimSpace(key) == "" {
		return idempotencyReservationRecord{}, false, nil
	}
	current := &corev1.ConfigMap{}
	if err := s.client.Get(ctx, clientKey(s.namespace, idempotencyReservationName(actorID, key)), current); err != nil {
		if apierrors.IsNotFound(err) {
			return idempotencyReservationRecord{}, false, nil
		}
		return idempotencyReservationRecord{}, false, err
	}
	return reservationRecordFromConfigMap(current), true, nil
}

func (s *idempotencyReservationStore) create(ctx context.Context, actorID, key string, record idempotencyReservationRecord) error {
	if strings.TrimSpace(actorID) == "" || strings.TrimSpace(key) == "" {
		return nil
	}
	return s.client.Create(ctx, reservationConfigMap(s.namespace, actorID, key, record))
}

func (s *idempotencyReservationStore) update(ctx context.Context, actorID, key string, mutate func(*idempotencyReservationRecord) error) (idempotencyReservationRecord, error) {
	record := idempotencyReservationRecord{}
	err := retry.RetryOnConflict(retry.DefaultRetry, func() error {
		current := &corev1.ConfigMap{}
		if err := s.client.Get(ctx, clientKey(s.namespace, idempotencyReservationName(actorID, key)), current); err != nil {
			return err
		}
		updated := reservationRecordFromConfigMap(current)
		if err := mutate(&updated); err != nil {
			return err
		}
		writeReservationRecordToConfigMap(current, updated)
		if err := s.client.Update(ctx, current); err != nil {
			return err
		}
		record = updated
		return nil
	})
	if err != nil {
		return idempotencyReservationRecord{}, err
	}
	return record, nil
}

func reservationConfigMap(namespace, actorID, key string, record idempotencyReservationRecord) *corev1.ConfigMap {
	configMap := &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{
			Name:      idempotencyReservationName(actorID, key),
			Namespace: namespace,
			Labels: map[string]string{
				actorLabelKey:       actorLabelValue(actorID),
				idempotencyLabelKey: idempotencyLabelValue(key),
			},
		},
	}
	writeReservationRecordToConfigMap(configMap, record)
	return configMap
}

func reservationRecordFromConfigMap(configMap *corev1.ConfigMap) idempotencyReservationRecord {
	if configMap == nil {
		return idempotencyReservationRecord{}
	}
	return idempotencyReservationRecord{
		fingerprint: strings.TrimSpace(configMap.Data[idempotencyReservationHashKey]),
		name:        strings.TrimSpace(configMap.Data[idempotencyReservationNameKey]),
		payload:     strings.TrimSpace(configMap.Data[idempotencyReservationBodyKey]),
		completed:   strings.EqualFold(strings.TrimSpace(configMap.Data[idempotencyReservationDoneKey]), "true"),
	}
}

func writeReservationRecordToConfigMap(configMap *corev1.ConfigMap, record idempotencyReservationRecord) {
	if configMap.Data == nil {
		configMap.Data = map[string]string{}
	}
	configMap.Data[idempotencyReservationHashKey] = strings.TrimSpace(record.fingerprint)
	configMap.Data[idempotencyReservationNameKey] = strings.TrimSpace(record.name)
	configMap.Data[idempotencyReservationBodyKey] = strings.TrimSpace(record.payload)
	if record.completed {
		configMap.Data[idempotencyReservationDoneKey] = "true"
	} else {
		configMap.Data[idempotencyReservationDoneKey] = "false"
	}
}
