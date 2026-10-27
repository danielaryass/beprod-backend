package mail_test

import (
	"testing"

	"beprod/internal/mail"
)

// Compile-time interface checks.
var _ mail.Mailer = &mail.FakeMailer{}
var _ mail.Mailer = mail.LogMailer{}

func TestFakeMailer_RecordsMessages(t *testing.T) {
	f := &mail.FakeMailer{}
	err := f.Send(mail.Message{To: "a@x.com", Subject: "s1", Body: "b1"})
	if err != nil {
		t.Fatalf("Send returned unexpected error: %v", err)
	}
	err = f.Send(mail.Message{To: "b@x.com", Subject: "s2", Body: "b2"})
	if err != nil {
		t.Fatalf("Send returned unexpected error: %v", err)
	}
	if f.Count() != 2 {
		t.Errorf("Count() = %d, want 2", f.Count())
	}
	if f.Sent[0].To != "a@x.com" {
		t.Errorf("Sent[0].To = %q, want %q", f.Sent[0].To, "a@x.com")
	}
}

func TestFakeMailer_Last(t *testing.T) {
	f := &mail.FakeMailer{}
	_, ok := f.Last()
	if ok {
		t.Error("Last() on empty FakeMailer should return ok=false")
	}

	f.Send(mail.Message{To: "a@x.com", Subject: "s1", Body: "b1"})
	f.Send(mail.Message{To: "b@x.com", Subject: "s2", Body: "b2"})

	msg, ok := f.Last()
	if !ok {
		t.Error("Last() should return ok=true after sending messages")
	}
	if msg.To != "b@x.com" {
		t.Errorf("Last().To = %q, want %q", msg.To, "b@x.com")
	}
}

func TestFakeMailer_ImplementsMailer(t *testing.T) {
	// Interface satisfaction is checked at compile time via the package-level vars above.
	// This test just confirms the test file is executed.
}

func TestLogMailer_SendReturnsNil(t *testing.T) {
	err := mail.LogMailer{}.Send(mail.Message{To: "a@x.com", Subject: "s", Body: "b"})
	if err != nil {
		t.Errorf("LogMailer.Send() returned %v, want nil", err)
	}
}
