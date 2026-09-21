package mcpserv

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"strconv"
	"time"

	"github.com/rezzminator/professor/pfm/internal/clock"
)

const (
	proxyCancelledCode          = -32800
	proxyRequestAdmissionWindow = 10 * time.Millisecond
)

type proxyRequestState struct {
	id                 json.RawMessage
	key                string
	ctx                context.Context
	cancel             context.CancelFunc
	admission          chan struct{}
	admissionTimer     clock.Timer
	predecessor        <-chan struct{}
	done               chan struct{}
	admitted           bool
	started, cancelled bool
	cancelSent         chan struct{}
	notificationOnly   bool
}

func (proxy *stdioProxy) registerRequest(ctx context.Context, id json.RawMessage) (*proxyRequestState, bool) {
	key, ok := proxyRequestKey(id)
	if !ok {
		return nil, false
	}
	requestCtx, cancel := context.WithCancel(ctx)
	state := &proxyRequestState{
		id: append(json.RawMessage(nil), id...), key: key, ctx: requestCtx, cancel: cancel,
		admission: make(chan struct{}), done: make(chan struct{}),
	}
	proxy.requestMutex.Lock()
	previous := proxy.requestTails[key]
	if previous != nil {
		state.predecessor = previous.done
	}
	overlapping := len(proxy.requestTails) != 0
	proxy.requestTails[key] = state
	if previous == nil {
		proxy.requests[key] = state
	}
	if !overlapping {
		state.admitted = true
		close(state.admission)
	}
	proxy.requestMutex.Unlock()
	if overlapping {
		state.admissionTimer = proxy.clock.NewTimer(proxyRequestAdmissionWindow)
	}
	return state, true
}

func (proxy *stdioProxy) resolvePendingAdmission(state *proxyRequestState, frame proxyFrame) {
	if state == nil {
		return
	}
	if key, ok := proxyCancellationKey(frame); ok && key == state.key {
		return
	}
	proxy.admitRequest(state)
}

func (proxy *stdioProxy) admitRequest(state *proxyRequestState) {
	proxy.requestMutex.Lock()
	if state.admitted || state.cancelled {
		proxy.requestMutex.Unlock()
		return
	}
	state.admitted = true
	close(state.admission)
	timer := state.admissionTimer
	proxy.requestMutex.Unlock()
	if timer != nil {
		timer.Stop()
	}
}

func (proxy *stdioProxy) forwardRequest(raw []byte, output io.Writer, state *proxyRequestState) {
	defer close(state.done)
	if !proxy.waitForRequestAdmission(state) || !waitForProxyRequest(state.ctx, state.predecessor) {
		proxy.completeRequest(state)
		return
	}
	proxy.requestMutex.Lock()
	if state.cancelled {
		proxy.requestMutex.Unlock()
		proxy.completeRequest(state)
		return
	}
	proxy.requests[state.key] = state
	state.started = true
	proxy.requestMutex.Unlock()
	defer proxy.completeRequest(state)
	proxy.forward(state.ctx, raw, output)
}

func (proxy *stdioProxy) waitForRequestAdmission(state *proxyRequestState) bool {
	if state.admissionTimer == nil {
		return waitForProxyRequest(state.ctx, state.admission)
	}
	select {
	case <-state.ctx.Done():
		return false
	case <-state.admission:
		return true
	case <-state.admissionTimer.C():
		proxy.admitRequest(state)
		return waitForProxyRequest(state.ctx, state.admission)
	}
}

func waitForProxyRequest(ctx context.Context, ready <-chan struct{}) bool {
	if ready == nil {
		return true
	}
	select {
	case <-ctx.Done():
		return false
	case <-ready:
		return true
	}
}

func (proxy *stdioProxy) cancelRequest(frame proxyFrame, output io.Writer) (bool, *proxyRequestState) {
	key, ok := proxyCancellationKey(frame)
	if !ok {
		return false, nil
	}
	proxy.requestMutex.Lock()
	state := proxy.requests[key]
	if state == nil {
		state = &proxyRequestState{
			key: key, done: make(chan struct{}), cancelSent: make(chan struct{}), notificationOnly: true,
		}
		if previous := proxy.requestTails[key]; previous != nil {
			state.predecessor = previous.done
		}
		proxy.requestTails[key] = state
		proxy.requestMutex.Unlock()
		return false, state
	}
	if state.cancelled {
		proxy.requestMutex.Unlock()
		return true, nil
	}
	state.cancelled = true
	if !state.started {
		if proxy.requests[key] == state {
			delete(proxy.requests, key)
		}
		if proxy.requestTails[key] == state {
			delete(proxy.requestTails, key)
		}
		timer := state.admissionTimer
		proxy.requestMutex.Unlock()
		state.cancel()
		if timer != nil {
			timer.Stop()
		}
		proxy.answerError(output, state.id, proxyCancelledCode, "request cancelled before daemon submission")
		return true, nil
	}
	state.cancelSent = make(chan struct{})
	proxy.requestMutex.Unlock()
	state.cancel()
	return false, state
}

func (proxy *stdioProxy) completeCancellation(state *proxyRequestState) {
	close(state.cancelSent)
	if !state.notificationOnly {
		return
	}
	if state.predecessor != nil {
		<-state.predecessor
	}
	proxy.requestMutex.Lock()
	if proxy.requestTails[state.key] == state {
		delete(proxy.requestTails, state.key)
	}
	proxy.requestMutex.Unlock()
	close(state.done)
}

func (proxy *stdioProxy) completeRequest(state *proxyRequestState) {
	proxy.requestMutex.Lock()
	if cancelSent := state.cancelSent; cancelSent != nil {
		proxy.requestMutex.Unlock()
		<-cancelSent
		proxy.requestMutex.Lock()
	}
	if proxy.requests[state.key] == state {
		delete(proxy.requests, state.key)
	}
	if proxy.requestTails[state.key] == state {
		delete(proxy.requestTails, state.key)
	}
	timer := state.admissionTimer
	proxy.requestMutex.Unlock()
	state.cancel()
	if timer != nil {
		timer.Stop()
	}
}

func proxyCancellationKey(frame proxyFrame) (string, bool) {
	if frame.Method != "notifications/cancelled" {
		return "", false
	}
	var params map[string]json.RawMessage
	if err := json.Unmarshal(frame.Params, &params); err != nil {
		return "", false
	}
	return proxyRequestKey(params["requestId"])
}

func proxyRequestKey(raw json.RawMessage) (string, bool) {
	value := bytes.TrimSpace(raw)
	if len(value) == 0 || bytes.Equal(value, []byte("null")) {
		return "", false
	}
	if value[0] == '"' {
		var id string
		if err := json.Unmarshal(value, &id); err != nil {
			return "", false
		}
		return "string:" + id, true
	}
	var number json.Number
	if err := json.Unmarshal(value, &number); err != nil {
		return "", false
	}
	id, err := strconv.ParseInt(number.String(), 10, 64)
	if err != nil {
		return "", false
	}
	return "number:" + strconv.FormatInt(id, 10), true
}
