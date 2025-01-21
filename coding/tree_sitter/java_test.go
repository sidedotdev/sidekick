package tree_sitter

import (
	"os"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestGetFileSignaturesStringJava(t *testing.T) {
	testCases := []struct {
		name     string
		code     string
		expected string
	}{
		{
			name:     "empty",
			code:     "",
			expected: "",
		},
		{
			name:     "simple class",
			code:     "class TestClass {}",
			expected: "class TestClass\n---\n",
		},
		{
			name:     "class with private field",
			code:     "class TestClass { private int field; }",
			expected: "class TestClass\n---\n",
		},
		{
			name:     "class with public field",
			code:     "class TestClass { public int field; }",
			expected: "class TestClass\n\tpublic int field;\n---\n",
		},
		{
			name:     "class with mixed fields",
			code:     "class TestClass { private int field1; public String field2; protected int field3; }",
			expected: "class TestClass\n\tpublic String field2;\n---\n",
		},
		{
			name:     "class with constructor",
			code:     "class TestClass { public TestClass() {} }",
			expected: "class TestClass\n\tpublic TestClass()\n---\n",
		},
		{
			name:     "class with parameterized constructor",
			code:     "class TestClass { public TestClass(int param1, String param2) {} }",
			expected: "class TestClass\n\tpublic TestClass(int param1, String param2)\n---\n",
		},
		{
			name:     "class with method",
			code:     "class TestClass { public void testMethod() {} }",
			expected: "class TestClass\n\tpublic void testMethod()\n---\n",
		},
		{
			name:     "class with multiple methods",
			code:     "class TestClass { public void testMethod() {}\npublic void testMethod2() {} }",
			expected: "class TestClass\n\tpublic void testMethod()\n\tpublic void testMethod2()\n---\n",
		},
		{
			name:     "class with parameterized method",
			code:     "class TestClass { public boolean testMethod(int param1, String param2) { return true; } }",
			expected: "class TestClass\n\tpublic boolean testMethod(int param1, String param2)\n---\n",
		},
		{
			name:     "class with private method",
			code:     "class TestClass { private void testMethod() {} }",
			expected: "class TestClass\n---\n",
		},
		{
			name:     "class with comment",
			code:     "// Test class comment\nclass TestClass {}",
			expected: "class TestClass\n---\n",
		},
		{
			name:     "multiple classes",
			code:     "class Class1 {} class Class2 {}",
			expected: "class Class1\n---\nclass Class2\n---\n",
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			// Create a temporary file with the test case code
			tmpfile, err := os.CreateTemp("", "test*.java")
			if err != nil {
				t.Fatalf("Failed to create temp file: %v", err)
			}
			defer os.Remove(tmpfile.Name())

			if _, err := tmpfile.Write([]byte(tc.code)); err != nil {
				t.Fatalf("Failed to write to temp file: %v", err)
			}
			if err := tmpfile.Close(); err != nil {
				t.Fatalf("Failed to close temp file: %v", err)
			}

			// Call GetFileSignatures with the path to the temp file
			result, err := GetFileSignaturesString(tmpfile.Name())
			if err != nil {
				t.Fatalf("GetFileSignatures returned an error: %v", err)
			}

			// Check the result
			if result != tc.expected {
				t.Errorf("GetFileSignatures returned incorrect result. Expected:\n%s\nGot:\n%s", tc.expected, result)
			}
		})
	}
}

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
