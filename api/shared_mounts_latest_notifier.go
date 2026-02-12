package main

import "sync"

// sharedMountsLatestNotifier provides a minimal in-process notification mechanism for
// long-polling shared mount "latest" requests.
//
// This intentionally does not persist state; callers should always re-fetch the latest
// manifest after being notified.
type sharedMountsLatestNotifier struct {
	mu      sync.Mutex
	waiters map[string]map[chan struct{}]struct{}
}

func newSharedMountsLatestNotifier() *sharedMountsLatestNotifier {
	return &sharedMountsLatestNotifier{waiters: map[string]map[chan struct{}]struct{}{}}
}

func (n *sharedMountsLatestNotifier) subscribe(key string) chan struct{} {
	ch := make(chan struct{})
	n.mu.Lock()
	defer n.mu.Unlock()
	waiters := n.waiters[key]
	if waiters == nil {
		waiters = map[chan struct{}]struct{}{}
		n.waiters[key] = waiters
	}
	waiters[ch] = struct{}{}
	return ch
}

func (n *sharedMountsLatestNotifier) unsubscribe(key string, ch chan struct{}) {
	if ch == nil {
		return
	}
	n.mu.Lock()
	defer n.mu.Unlock()
	waiters := n.waiters[key]
	if waiters == nil {
		return
	}
	delete(waiters, ch)
	if len(waiters) == 0 {
		delete(n.waiters, key)
	}
}

func (n *sharedMountsLatestNotifier) notify(key string) {
	n.mu.Lock()
	waiters := n.waiters[key]
	delete(n.waiters, key)
	n.mu.Unlock()

	for ch := range waiters {
		// Closing unblocks all long-pollers for this key.
		close(ch)
	}
}
