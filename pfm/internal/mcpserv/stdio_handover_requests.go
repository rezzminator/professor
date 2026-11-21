package mcpserv

// cancellation carries the client's notifications/cancelled to whoever holds
// the request it names: a retired child that has not answered it (carried), or
// the image this server started on (front, left to the caller); neither when
// no one holds it, the serving child then taking it.
func (handover *stdioHandover) cancellation(
	frame []byte,
	envelope proxyFrame,
	current *handoverChild,
) (carried, front bool) {
	key, ok := proxyCancellationKey(envelope)
	if !ok {
		return false, false
	}
	if holder := handover.holder(key, current); holder != nil {
		holder.cancel(frame, key)
		return true, false
	}
	if handover.front[key] {
		delete(handover.front, key)
		return false, true
	}
	return false, false
}

// recordFront notes a client request the image this server started on takes.
func (handover *stdioHandover) recordFront(envelope proxyFrame) {
	key, ok := proxyRequestKey(envelope.ID)
	if !ok || envelope.Method == "" {
		return
	}
	if handover.front == nil {
		handover.front = map[string]bool{}
	}
	handover.front[key] = true
}

// holder is the retired child still holding the client request key; nil when
// none does.
func (handover *stdioHandover) holder(key string, current *handoverChild) *handoverChild {
	handover.mutex.Lock()
	children := append([]*handoverChild(nil), handover.children...)
	handover.mutex.Unlock()
	for _, child := range children {
		if child != current && child.holds(key) {
			return child
		}
	}
	return nil
}

// cancel carries the client's notifications/cancelled for key to the child
// holding that request: a cancelled request may go unanswered, so it no longer
// holds a retiring child's input open.
func (child *handoverChild) cancel(frame []byte, key string) {
	if err := child.write(frame); err != nil {
		child.handover.warn("carry a cancellation to the installed pfm %s: %v", child.image.Path, err)
	}
	child.settle(key)
}

// holds reports whether the child has not answered the client request key yet.
func (child *handoverChild) holds(key string) bool {
	child.mutex.Lock()
	defer child.mutex.Unlock()
	return child.pending[key]
}

// settle forgets the client request key, ending a retiring child's input once
// nothing is left.
func (child *handoverChild) settle(key string) {
	child.mutex.Lock()
	delete(child.pending, key)
	drained := child.retiring && len(child.pending) == 0
	child.mutex.Unlock()
	if drained {
		child.closeStdin()
	}
}
