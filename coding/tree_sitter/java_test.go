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
			name:     "empty interface",
			code:     "interface TestInterface {}",
			expected: "interface TestInterface\n---\n",
		},
		{
			name:     "interface with method",
			code:     "interface TestInterface { void testMethod(); }",
			expected: "interface TestInterface\n\tvoid testMethod();\n---\n",
		},
		{
			name:     "interface with multiple methods",
			code:     "interface TestInterface { void method1(); String method2(int param); }",
			expected: "interface TestInterface\n\tvoid method1();\n\tString method2(int param);\n---\n",
		},
		{
			name:     "public interface",
			code:     "public interface TestInterface { void method(); }",
			expected: "public interface TestInterface\n\tvoid method();\n---\n",
		},
		{
			name:     "interface with constant",
			code:     "interface TestInterface { public static final int CONSTANT = 42; void method(); }",
			expected: "interface TestInterface\n\tpublic static final int CONSTANT = 42;\n\tvoid method();\n---\n",
		},
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
		{
			name:     "annotation type declaration",
			code:     "@interface TestAnnotation { String value(); int count() default 0; }",
			expected: "@interface TestAnnotation\n\tString value();\n\tint count() default 0;\n---\n",
		},
		{
			name:     "annotation type declaration with private method",
			code:     "@interface TestAnnotation { String value(); private int count() default 0; }",
			expected: "@interface TestAnnotation\n\tString value();\n---\n",
		},
		{
			name:     "class with annotation",
			code:     "@Test class TestClass {}",
			expected: "@Test class TestClass\n---\n",
		},
		{
			name:     "method with annotation",
			code:     "class TestClass { @Override public void testMethod() {} }",
			expected: "class TestClass\n\t@Override public void testMethod()\n---\n",
		},
		{
			name:     "interface with annotation",
			code:     "@FunctionalInterface interface TestInterface { void test(); }",
			expected: "@FunctionalInterface interface TestInterface\n\tvoid test();\n---\n",
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
			expected: "Person",
		},
		{
			name:     "empty",
			code:     "",
			expected: "",
		},
		{
			name:     "empty class",
			code:     "class Test {}",
			expected: "Test",
		},
		{
			name: "class with private, public and protected fields",
			code: `
public class TestClass {
    private int field1;
    public String field2;
    protected String field3;
}`,
			expected: "TestClass",
		},
		{
			name: "class with methods and fields",
			code: `
public class Complex {
    private double real;
    private double imaginary;
	public double x;

    public Complex add(Complex other) {
        return new Complex();
    }

    public Complex subtract(Complex other) {
        return new Complex();
    }

    private Complex internal(Complex other) {
        return new Complex();
    }
}`,
			expected: "Complex, add, subtract",
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
