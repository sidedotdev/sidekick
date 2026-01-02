package flow_action

import (
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

		if gs.GetPendingUserAction() != nil {
			t.Error("Action should be nil after consume")
		}
	})

	t.Run("consume when nil", func(t *testing.T) {
		gs := &GlobalState{}
		// Should not panic
		gs.ConsumePendingUserAction()
	})
}
