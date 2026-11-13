// Package notifyudp is the best-effort UDP new-chapter notifier: one JSON
// object per datagram, soft-state subscriptions with a TTL. It implements
// api.ChapterNotifier and never promises delivery.
package notifyudp
