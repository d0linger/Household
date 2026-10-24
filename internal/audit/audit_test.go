package audit

import (
	"context"
	"testing"

	"household/internal/value"
)

func TestContextIdentity(t *testing.T) {
	t.Parallel()
	user, household, request := value.UUID(), value.UUID(), value.UUID()
	ctx := WithActor(WithRequest(context.Background(), request), user, household)
	actor, err := validated(ctx)
	if err != nil || actor.userID != user || actor.householdID != household || actor.requestID != request || actor.kind != "user" {
		t.Fatalf("bound context = %#v, %v", actor, err)
	}
	worker, err := validated(WithSystem(ctx, "worker"))
	if err != nil || worker.userID != "" || worker.householdID != "" || worker.requestID != request || worker.kind != "worker" {
		t.Fatalf("worker retained user identity: %#v, %v", worker, err)
	}
	unchanged, err := validated(ctx)
	if err != nil || unchanged != actor {
		t.Fatal("deriving worker context changed the original request")
	}
	operator, err := validated(WithHousehold(WithSystem(ctx, "operator"), household))
	if err != nil || operator.userID != "" || operator.householdID != household || operator.kind != "operator" {
		t.Fatalf("operator scope claims a user: %#v, %v", operator, err)
	}
}

func TestContextRejectsInvalidIdentity(t *testing.T) {
	t.Parallel()
	uid, hid := value.UUID(), value.UUID()
	tests := []struct {
		name string
		ctx  context.Context
	}{
		{"user", WithActor(context.Background(), "invalid", hid)},
		{"household", WithActor(context.Background(), uid, "")},
		{"request", WithRequest(context.Background(), "injected request")},
		{"kind", WithSystem(context.Background(), "untrusted")},
		{"operator household", WithHousehold(WithSystem(context.Background(), "operator"), "invalid")},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if _, err := validated(test.ctx); err == nil {
				t.Fatal("invalid context accepted")
			}
		})
	}
}

func TestSystemContextGeneratesIndependentRequestIDs(t *testing.T) {
	t.Parallel()
	first, err := validated(context.Background())
	if err != nil || !value.ValidUUID(first.requestID) || first.kind != "system" {
		t.Fatalf("default system context invalid: %#v, %v", first, err)
	}
	second, err := validated(context.Background())
	if err != nil || second.requestID == first.requestID {
		t.Fatal("independent transactions reused an implicit request ID")
	}
}
