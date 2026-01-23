package main

import (
	"sync"
	"time"

	"golang.org/x/time/rate"
)

type sshMintLimiter struct {
	mu              sync.Mutex
	limit           rate.Limit
	burst           int
	bucketTTL       time.Duration
	cleanupInterval time.Duration
	lastCleanup     time.Time
	buckets         map[string]*sshMintBucket
}

type sshMintBucket struct {
	limiter  *rate.Limiter
	lastUsed time.Time
}

func newSSHMintLimiter() *sshMintLimiter {
	limit := parseIntEnvAllowZero("SPRITZ_SSH_MINT_LIMIT", 5)
	window := parseDurationEnv("SPRITZ_SSH_MINT_WINDOW", time.Minute)
	if limit <= 0 || window <= 0 {
		return nil
	}
	burst := parseIntEnv("SPRITZ_SSH_MINT_BURST", limit)
	rateLimit := rate.Limit(float64(limit) / window.Seconds())
	if rateLimit <= 0 {
		return nil
	}
	if burst <= 0 {
		burst = limit
	}
	bucketTTL := parseDurationEnv("SPRITZ_SSH_MINT_BUCKET_TTL", 30*time.Minute)
	cleanupInterval := parseDurationEnv("SPRITZ_SSH_MINT_BUCKET_CLEANUP", 5*time.Minute)
	if bucketTTL <= 0 {
		bucketTTL = 0
		cleanupInterval = 0
	} else {
		if cleanupInterval <= 0 || cleanupInterval > bucketTTL {
			cleanupInterval = bucketTTL
		}
	}
	return &sshMintLimiter{
		limit:           rateLimit,
		burst:           burst,
		bucketTTL:       bucketTTL,
		cleanupInterval: cleanupInterval,
		lastCleanup:     time.Now(),
		buckets:         make(map[string]*sshMintBucket),
	}
}

func (l *sshMintLimiter) Allow(key string) bool {
	if l == nil {
		return true
	}
	if key == "" {
		return true
	}
	now := time.Now()
	l.mu.Lock()
	if l.bucketTTL > 0 && l.cleanupInterval > 0 && now.Sub(l.lastCleanup) >= l.cleanupInterval {
		for bucketKey, bucket := range l.buckets {
			if now.Sub(bucket.lastUsed) >= l.bucketTTL {
				delete(l.buckets, bucketKey)
			}
		}
		l.lastCleanup = now
	}
	bucket := l.buckets[key]
	if bucket == nil {
		bucket = &sshMintBucket{limiter: rate.NewLimiter(l.limit, l.burst)}
		l.buckets[key] = bucket
	}
	bucket.lastUsed = now
	allowed := bucket.limiter.Allow()
	l.mu.Unlock()
	return allowed
}
