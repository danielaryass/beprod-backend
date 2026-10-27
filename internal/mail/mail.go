package mail

import (
	"log"
	"sync"
)

// Message represents an email message.
type Message struct{ To, Subject, Body string }

// Mailer is the interface for sending email.
type Mailer interface{ Send(m Message) error }

// LogMailer prints the email to the server log. Real provider = later task.
type LogMailer struct{}

// Send logs the message to the server log and returns nil.
func (LogMailer) Send(m Message) error {
	log.Printf("[mail] to=%s subject=%q\n%s", m.To, m.Subject, m.Body)
	return nil
}

// FakeMailer records messages in memory (used by tests). Always used via pointer.
type FakeMailer struct {
	mu   sync.Mutex
	Sent []Message
}

// Send locks mu, appends m to Sent, and returns nil.
func (f *FakeMailer) Send(m Message) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.Sent = append(f.Sent, m)
	return nil
}

// Last returns the last message and true; returns (Message{}, false) when Sent is empty.
func (f *FakeMailer) Last() (Message, bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.Sent) == 0 {
		return Message{}, false
	}
	return f.Sent[len(f.Sent)-1], true
}

// Count returns the number of messages sent.
func (f *FakeMailer) Count() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.Sent)
}
