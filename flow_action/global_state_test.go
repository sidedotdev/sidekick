package flow_action

import (
	"reflect"
	"strings"
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

func TestGlobalState_NamedCancellationIsolation(t *testing.T) {
	gs := &GlobalState{}
	var broadCanceled, streamCanceled, otherCanceled int

	gs.AddCancelFunc(func() {
		broadCanceled++
	})
	gs.AddNamedCancelFunc("llm_stream", func() {
		streamCanceled++
	})
	gs.AddNamedCancelFunc("other", func() {
		otherCanceled++
	})

	gs.CancelNamed("llm_stream")

	if broadCanceled != 0 {
		t.Fatalf("Expected broad cancellation queue to remain untouched, got %d calls", broadCanceled)
	}
	if streamCanceled != 1 {
		t.Fatalf("Expected stream cancellation once, got %d calls", streamCanceled)
	}
	if otherCanceled != 0 {
		t.Fatalf("Expected unrelated named queue to remain untouched, got %d calls", otherCanceled)
	}

	gs.CancelNamed("llm_stream")
	if streamCanceled != 1 {
		t.Fatalf("Expected canceled named queue to be cleared, got %d calls", streamCanceled)
	}

	gs.Cancel()
	if broadCanceled != 1 {
		t.Errorf("Expected broad cancellation once, got %d calls", broadCanceled)
	}
	if otherCanceled != 1 {
		t.Errorf("Expected broad cancellation to include remaining named queues, got %d calls", otherCanceled)
	}

	gs.Cancel()
	if broadCanceled != 1 || otherCanceled != 1 {
		t.Errorf("Expected broad cancellation queues to be cleared, got broad=%d other=%d", broadCanceled, otherCanceled)
	}
}
func TestGlobalState_NamedCancellationGenerationAndCleanup(t *testing.T) {
	t.Parallel()

	gs := &GlobalState{}
	canceled := 0
	unregister := gs.RegisterNamedCancelFunc("llm_stream", func() {
		canceled++
	})
	unregister()

	gs.CancelNamed("llm_stream")
	if canceled != 0 {
		t.Fatalf("Expected completed registration not to be canceled, got %d calls", canceled)
	}
	if generation := gs.NamedCancellationGeneration("llm_stream"); generation != 1 {
		t.Fatalf("Expected named cancellation generation 1, got %d", generation)
	}

	gs.RegisterNamedCancelFunc("llm_stream", func() {
		canceled++
	})
	gs.Cancel()
	if canceled != 1 {
		t.Fatalf("Expected broad cancellation to include active named registration, got %d calls", canceled)
	}
	if generation := gs.NamedCancellationGeneration("llm_stream"); generation != 1 {
		t.Fatalf("Expected broad cancellation not to advance named generation, got %d", generation)
	}

	gs.CancelNamed("llm_stream")
	if generation := gs.NamedCancellationGeneration("llm_stream"); generation != 2 {
		t.Fatalf("Expected repeated named cancellation generation 2, got %d", generation)
	}
}
func TestGlobalState_CancellationOrderIsDeterministic(t *testing.T) {
	t.Parallel()

	t.Run("named queue", func(t *testing.T) {
		gs := &GlobalState{}
		var canceled []int
		for i := 1; i <= 4; i++ {
			i := i
			gs.RegisterNamedCancelFunc("llm_stream", func() {
				canceled = append(canceled, i)
			})
		}

		gs.CancelNamed("llm_stream")

		expected := []int{1, 2, 3, 4}
		if !reflect.DeepEqual(canceled, expected) {
			t.Fatalf("Expected registration order %v, got %v", expected, canceled)
		}
	})

	t.Run("broad cancellation sorts names then registrations", func(t *testing.T) {
		gs := &GlobalState{}
		var canceled []string
		for _, registration := range []string{"beta-1", "alpha-1", "beta-2", "alpha-2"} {
			registration := registration
			name := strings.SplitN(registration, "-", 2)[0]
			gs.RegisterNamedCancelFunc(name, func() {
				canceled = append(canceled, registration)
			})
		}

		gs.Cancel()

		expected := []string{"alpha-1", "alpha-2", "beta-1", "beta-2"}
		if !reflect.DeepEqual(canceled, expected) {
			t.Fatalf("Expected deterministic order %v, got %v", expected, canceled)
		}
	})
}
