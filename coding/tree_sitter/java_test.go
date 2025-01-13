package tree_sitter

import (
	"os"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestGetFileSymbolsStringJava(t *testing.T) {
	tests := []struct {
		name     string
		code     string
		expected string
	}{
		{
			name: "simple class with method",
			code: `
public class Test {
    public void testMethod() {
        System.out.println("Hello");
    }
}`,
			expected: "Test, testMethod",
		},
		{
			name: "class with constructor",
			code: `
public class Person {
    public Person(String name) {
        // Constructor
    }
}`,
			expected: "Person, Person",
		},
		{
			name:     "empty",
			code:     "",
			expected: "",
		},
		{
			name:     "single class",
			code:     "class Test {}",
			expected: "Test",
		},
		{
			name: "class with fields",
			code: `
public class TestClass {
    private int field1;
    protected String field2;
}`,
			expected: "TestClass, field1, field2",
		},
		{
			name: "class with method and fields",
			code: `
public class Complex {
    private double real;
    private double imaginary;

    public Complex add(Complex other) {
        return new Complex();
    }
}`,
			expected: "Complex, real, imaginary, add",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			tmpfile, err := os.CreateTemp("", "*.java")
			if err != nil {
				t.Fatalf("Failed to create temp file: %v", err)
			}
			defer os.Remove(tmpfile.Name())

			if _, err := tmpfile.Write([]byte(test.code)); err != nil {
				t.Fatalf("Failed to write to temp file: %v", err)
			}
			if err := tmpfile.Close(); err != nil {
				t.Fatalf("Failed to close temp file: %v", err)
			}

			symbolsString, err := GetFileSymbolsString(tmpfile.Name())
			if err != nil {
				t.Fatalf("Failed to get symbols: %v", err)
			}

			assert.Equal(t, test.expected, symbolsString)
		})
	}
}
