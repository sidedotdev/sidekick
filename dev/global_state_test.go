package dev

import (
	"sync"
	"testing"
)

func TestGlobalState_UserActions(t *testing.T) {
	t.Run("initial state is nil", func(t *testing.T) {
		gs := &GlobalState{}
		if action := gs.GetPendingUserAction(); action != nil {
			t.Errorf("Expected no pending action initially, got %v", *action)
		}
	})

	t.Run("set and get action", func(t *testing.T) {
		gs := &GlobalState{}
		actionToSet := UserActionGoNext
		gs.SetUserAction(actionToSet)

		retrievedAction := gs.GetPendingUserAction()
		if retrievedAction == nil {
			t.Fatalf("Expected action %v, got nil", actionToSet)
		}
		if *retrievedAction != actionToSet {
			t.Errorf("Expected action %v, got %v", actionToSet, *retrievedAction)
		}
	})

	t.Run("overwrite action", func(t *testing.T) {
		gs := &GlobalState{}
		action1 := UserActionGoNext
		action2 := UserActionType("temporary_other_action_for_test") // A distinct value

		gs.SetUserAction(action1)
		retrievedAction1 := gs.GetPendingUserAction()
		if retrievedAction1 == nil || *retrievedAction1 != action1 {
			t.Fatalf("Expected action1 %v, got %v", action1, retrievedAction1)
		}

		gs.SetUserAction(action2)
		retrievedAction2 := gs.GetPendingUserAction()
		if retrievedAction2 == nil {
			t.Fatalf("Expected action %v after overwrite, got nil", action2)
		}
		if *retrievedAction2 != action2 {
			t.Errorf("Expected action %v after overwrite, got %v", action2, *retrievedAction2)
		}
	})

	t.Run("consume action", func(t *testing.T) {
		gs := &GlobalState{}
		actionToSet := UserActionGoNext
		gs.SetUserAction(actionToSet)

		// Ensure it's set
		if gs.GetPendingUserAction() == nil {
			t.Fatal("Action was not set before consume test")
		}

		gs.ConsumePendingUserAction()
		if action := gs.GetPendingUserAction(); action != nil {
			t.Errorf("Expected no pending action after consumption, got %v", *action)
		}
	})

	t.Run("consume when no action is set", func(t *testing.T) {
		gs := &GlobalState{}
		// Ensure no action is set
		if gs.GetPendingUserAction() != nil {
			t.Fatal("Action was set before 'consume when no action' test")
		}

		gs.ConsumePendingUserAction() // Should be a no-op
		if action := gs.GetPendingUserAction(); action != nil {
			t.Errorf("Expected no pending action after consuming nil, got %v", *action)
		}
	})
}

func TestGlobalState_UserActions_ConcurrentAccess(t *testing.T) {
	gs := &GlobalState{}
	var wg sync.WaitGroup
	numGoroutines := 50

	actionVal := UserActionGoNext

	for i := 0; i < numGoroutines; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			gs.SetUserAction(actionVal)
		}()

		wg.Add(1)
		go func() {
			defer wg.Done()
			_ = gs.GetPendingUserAction()
		}()

		wg.Add(1)
		go func() {
			defer wg.Done()
			gs.ConsumePendingUserAction()
		}()
	}

	wg.Wait()
	// Final check, mostly to ensure no panics and callable.
	// The state itself is non-deterministic here after mixed operations.
	_ = gs.GetPendingUserAction()
}
