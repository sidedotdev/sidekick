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

func TestGlobalState_Values(t *testing.T) {
	t.Run("get returns nil for missing key", func(t *testing.T) {
		gs := &GlobalState{}
		if value := gs.GetValue("nonexistent"); value != nil {
			t.Errorf("Expected nil for missing key, got %v", value)
		}
	})

	t.Run("get string returns empty for missing key", func(t *testing.T) {
		gs := &GlobalState{}
		if value := gs.GetStringValue("nonexistent"); value != "" {
			t.Errorf("Expected empty string for missing key, got %q", value)
		}
	})

	t.Run("set and get value", func(t *testing.T) {
		gs := &GlobalState{}
		gs.SetValue("testKey", "testValue")

		value := gs.GetValue("testKey")
		if value != "testValue" {
			t.Errorf("Expected 'testValue', got %v", value)
		}
	})

	t.Run("set and get string value", func(t *testing.T) {
		gs := &GlobalState{}
		gs.SetValue("branch", "main")

		value := gs.GetStringValue("branch")
		if value != "main" {
			t.Errorf("Expected 'main', got %q", value)
		}
	})

	t.Run("get string value returns empty for non-string", func(t *testing.T) {
		gs := &GlobalState{}
		gs.SetValue("number", 42)

		value := gs.GetStringValue("number")
		if value != "" {
			t.Errorf("Expected empty string for non-string value, got %q", value)
		}
	})

	t.Run("overwrite value", func(t *testing.T) {
		gs := &GlobalState{}
		gs.SetValue("key", "value1")
		gs.SetValue("key", "value2")

		value := gs.GetStringValue("key")
		if value != "value2" {
			t.Errorf("Expected 'value2' after overwrite, got %q", value)
		}
	})

	t.Run("multiple keys", func(t *testing.T) {
		gs := &GlobalState{}
		gs.SetValue("key1", "value1")
		gs.SetValue("key2", "value2")

		if gs.GetStringValue("key1") != "value1" {
			t.Errorf("Expected 'value1' for key1")
		}
		if gs.GetStringValue("key2") != "value2" {
			t.Errorf("Expected 'value2' for key2")
		}
	})

	t.Run("stores arbitrary types", func(t *testing.T) {
		gs := &GlobalState{}
		gs.SetValue("int", 42)
		gs.SetValue("bool", true)
		gs.SetValue("slice", []string{"a", "b"})

		if gs.GetValue("int") != 42 {
			t.Errorf("Expected 42 for int key")
		}
		if gs.GetValue("bool") != true {
			t.Errorf("Expected true for bool key")
		}
		slice, ok := gs.GetValue("slice").([]string)
		if !ok || len(slice) != 2 {
			t.Errorf("Expected []string{'a', 'b'} for slice key")
		}
	})
}
