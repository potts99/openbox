// SPDX-License-Identifier: AGPL-3.0-only

package httpapi

import (
	"io"
	"testing"
)

func TestDetachOutputIfOnlyClearsMatchingWriter(t *testing.T) {
	p := &persistentConsole{}

	_ = p.attachOutput()
	p.outMu.Lock()
	oldW := p.outW
	p.outMu.Unlock()

	rNew := p.attachOutput()
	p.outMu.Lock()
	newW := p.outW
	p.outMu.Unlock()
	if newW == nil || newW == oldW {
		t.Fatal("expected a distinct attached writer after reattach")
	}

	// Simulate the stdout pump observing a Write error on the previous pipe
	// after a newer bridge has already attached.
	p.detachOutputIf(oldW)

	p.outMu.Lock()
	still := p.outW
	p.outMu.Unlock()
	if still != newW {
		t.Fatal("detachOutputIf cleared a newer writer after an older write failure")
	}

	done := make(chan struct{})
	go func() {
		defer close(done)
		buf := make([]byte, 4)
		_, _ = rNew.Read(buf)
	}()

	if _, err := newW.Write([]byte("ok")); err != nil {
		t.Fatalf("newer writer should still accept writes: %v", err)
	}
	<-done

	p.detachOutputIf(newW)
	p.outMu.Lock()
	cleared := p.outW
	p.outMu.Unlock()
	if cleared != nil {
		t.Fatal("detachOutputIf should clear when identities match")
	}
}

func TestDetachOutputClosesCurrentWriter(t *testing.T) {
	p := &persistentConsole{}
	r := p.attachOutput()
	p.detachOutput()

	buf := make([]byte, 1)
	_, err := r.Read(buf)
	if err != io.EOF && err != io.ErrClosedPipe {
		t.Fatalf("read after detach: %v", err)
	}
	p.outMu.Lock()
	defer p.outMu.Unlock()
	if p.outW != nil {
		t.Fatal("expected outW cleared")
	}
}
