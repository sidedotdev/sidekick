package tree_sitter

import (
	"context"
	"os"
	"sidekick/utils"
	"strings"
	"testing"

	sitter "github.com/smacker/go-tree-sitter"
	"github.com/smacker/go-tree-sitter/java"
	"github.com/stretchr/testify/assert"
)

func parseJavaString(code string) *sitter.Tree {
	parser := sitter.NewParser()
	parser.SetLanguage(java.GetLanguage())
	tree, err := parser.ParseCtx(context.Background(), nil, []byte(code))
	if err != nil {
		panic(err)
	}
	return tree
}

func TestGetDeclarationIndentLevel(t *testing.T) {
	testCases := []struct {
		name     string
		code     string
		nodePath []string
		expected int
	}{
		{
			name:     "top level class",
			code:     "class Test {}",
			nodePath: []string{"class_declaration"},
			expected: 0,
		},
		{
			name:     "nested class",
			code:     "class Outer { class Inner {} }",
			nodePath: []string{"class_declaration", "class_body", "class_declaration"},
			expected: 1,
		},
		{
			name:     "deeply nested class",
			code:     "class L1 { class L2 { class L3 {} } }",
			nodePath: []string{"class_declaration", "class_body", "class_declaration", "class_body", "class_declaration"},
			expected: 2,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			tree := parseJavaString(tc.code)
			defer tree.Close()

			node := tree.RootNode()
			for _, pathElement := range tc.nodePath {
				found := false
				for i := 0; i < int(node.ChildCount()); i++ {
					child := node.Child(i)
					if child.Type() == pathElement {
						node = child
						found = true
						break
					}
				}
				assert.True(t, found, "Failed to find node of type %s", pathElement)
			}

			level := getDeclarationIndentLevel(node)
			assert.Equal(t, tc.expected, level)
		})
	}
}

func TestGetFileHeadersStringJava(t *testing.T) {
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
			name:     "single import",
			code:     "import java.util.List;",
			expected: "import java.util.List;\n",
		},
		{
			name:     "multiple imports",
			code:     "import java.util.List;\nimport java.util.Map;",
			expected: "import java.util.List;\nimport java.util.Map;\n",
		},
		{
			name:     "multiple imports on consecutive lines",
			code:     "import java.util.List;\nimport java.util.Map;\nimport java.util.Set;",
			expected: "import java.util.List;\nimport java.util.Map;\nimport java.util.Set;\n",
		},
		{
			name:     "static import",
			code:     "import static org.junit.Assert.*;",
			expected: "import static org.junit.Assert.*;\n",
		},
		{
			name:     "wildcard import",
			code:     "import java.util.*;",
			expected: "import java.util.*;\n",
		},
		{
			name:     "package declaration",
			code:     "package com.example;",
			expected: "package com.example;\n",
		},
		{
			name:     "package + import",
			code:     "package com.example;\nimport java.util.List;",
			expected: "package com.example;\nimport java.util.List;\n",
		},
		{
			name:     "package + empty line + import",
			code:     "package com.example;\n\nimport java.util.List;",
			expected: "package com.example;\n\nimport java.util.List;\n",
		},
		{
			name:     "package + multiple whitespace lines + import",
			code:     "package com.example;\n\n\t\t\n  \n \t \t\nimport java.util.List;",
			expected: "package com.example;\n\n\t\t\n  \n \t \t\nimport java.util.List;\n",
		},
		{
			name:     "package later in file",
			code:     "import java.util.List;\npackage com.example;",
			expected: "import java.util.List;\npackage com.example;\n",
		},
		{
			name:     "import later in file",
			code:     "package com.example;\nclass Main {}\nimport java.util.List;",
			expected: "package com.example;\n---\nimport java.util.List;\n",
		},
		{
			name:     "package twice in file",
			code:     "package com.example;\nclass Main {}\npackage com.other;",
			expected: "package com.example;\n---\npackage com.other;\n",
		},
		{
			name:     "import twice in file",
			code:     "import java.util.List;\nclass Main {}\nimport java.util.Map;",
			expected: "import java.util.List;\n---\nimport java.util.Map;\n",
		},
		{
			name:     "package + import twice in file",
			code:     "package com.example;\nimport java.util.List;\nclass Main {}\npackage com.other;\nimport java.util.Map;",
			expected: "package com.example;\nimport java.util.List;\n---\npackage com.other;\nimport java.util.Map;\n",
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

			result, err := GetFileHeadersString(tmpfile.Name(), 0)
			assert.Nil(t, err)

			// Check the result
			if result != tc.expected {
				t.Errorf("GetFileHeadersString returned incorrect result. Expected:\n%s\nGot:\n%s", utils.PanicJSON(tc.expected), utils.PanicJSON(result))
			}
		})
	}
}

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
			name: "nested class in class",
			code: `
public class OuterClass {
    public static class StaticNestedClass {
        public void nestedMethod() {}
    }
    protected class IgnoredNestedClass {}
    private class AnotherIgnoredClass {}
    public class PublicNestedClass {
        public void method() {}
    }
}`,
			expected: `public class OuterClass
---
	public static class StaticNestedClass
		public void nestedMethod()
---
	public class PublicNestedClass
		public void method()
---
`,
		},
		{
			name: "nested interface in class",
			code: `
public class OuterClass {
    public interface NestedInterface {
        void method();
    }
    private interface IgnoredInterface {}
}`,
			expected: `public class OuterClass
---
	public interface NestedInterface
		void method();
---
`,
		},
		{
			name: "nested annotation in class",
			code: `
public class OuterClass {
    public @interface NestedAnnotation {
        String value() default "";
    }
    private @interface IgnoredAnnotation {}
}`,
			expected: `public class OuterClass
---
	public @interface NestedAnnotation
		String value() default "";
---
`,
		},
		{
			name: "deeply nested types",
			code: `
public class OuterClass {
    public class Level1 {
        public interface Level2 {
            public class Level3 {
                void method();
            }
        }
    }
}`,
			expected: `public class OuterClass
---
	public class Level1
---
		public interface Level2
---
			public class Level3
				void method()
---
`,
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
			name:     "class with public constant",
			code:     "class TestClass { public static final int CONSTANT = 42; }",
			expected: "class TestClass\n\tpublic static final int CONSTANT = 42;\n---\n",
		},
		{
			name:     "class with multiple constants",
			code:     "class TestClass { private static final int CONSTANT1 = 42; public static final int CONSTANT2 = 43; protected static final int CONSTANT3 = 44; }",
			expected: "class TestClass\n\tpublic static final int CONSTANT2 = 43;\n---\n",
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
			name:     "method argument with annotation",
			code:     "class TestClass { public void testMethod(@NotNull String arg) {} }",
			expected: "class TestClass\n\tpublic void testMethod(@NotNull String arg)\n---\n",
		},
		{
			name:     "interface with annotation",
			code:     "@FunctionalInterface interface TestInterface { void test(); }",
			expected: "@FunctionalInterface interface TestInterface\n\tvoid test();\n---\n",
		},
		{
			name:     "class with type parameter",
			code:     "class Box<T> { }",
			expected: "class Box<T>\n---\n",
		},
		{
			name:     "class with bounded type parameter",
			code:     "class NumberBox<T extends Number> { }",
			expected: "class NumberBox<T extends Number>\n---\n",
		},
		{
			name:     "class with multiple type parameters",
			code:     "class Pair<K,V> { }",
			expected: "class Pair<K,V>\n---\n",
		},
		{
			name:     "class with complex type parameters",
			code:     "class Container<T extends Comparable<T>> { }",
			expected: "class Container<T extends Comparable<T>>\n---\n",
		},
		{
			name:     "generic interface",
			code:     "interface Box<T> { T get(); void put(T item); }",
			expected: "interface Box<T>\n\tT get();\n\tvoid put(T item);\n---\n",
		},
		{
			name:     "generic interface with bounds",
			code:     "interface Sortable<T extends Comparable<T>> { void sort(T[] items); }",
			expected: "interface Sortable<T extends Comparable<T>>\n\tvoid sort(T[] items);\n---\n",
		},
		{
			name:     "class with generic method",
			code:     "class Util { public <T> void print(T item) {} }",
			expected: "class Util\n\tpublic <T> void print(T item)\n---\n",
		},
		{
			name:     "class with bounded generic method",
			code:     "class NumberUtil { public <T extends Number> double sum(T[] numbers) {} }",
			expected: "class NumberUtil\n\tpublic <T extends Number> double sum(T[] numbers)\n---\n",
		},
		{
			name:     "class with multiple generic methods",
			code:     "class Converter { public <T,R> R convert(T input) {} public <V> void validate(V value) {} }",
			expected: "class Converter\n\tpublic <T,R> R convert(T input)\n\tpublic <V> void validate(V value)\n---\n",
		},
		{
			name:     "empty enum",
			code:     "enum EmptyEnum {}",
			expected: "enum EmptyEnum\n---\n",
		},
		{
			name:     "enum with constants",
			code:     "enum Direction { NORTH, SOUTH, EAST, WEST }",
			expected: "enum Direction\n\tNORTH\n\tSOUTH\n\tEAST\n\tWEST\n---\n",
		},
		{
			name:     "enum with public method",
			code:     "enum Status { OK, ERROR; public String getMessage() { return null; } }",
			expected: "enum Status\n\tOK\n\tERROR\n\tpublic String getMessage()\n---\n",
		},
		{
			name:     "enum with private method",
			code:     "enum Status { OK, ERROR; private String getMessage() { return null; } }",
			expected: "enum Status\n\tOK\n\tERROR\n---\n",
		},
		{
			name:     "enum with constants and multiple methods",
			code:     "enum Complex { FIRST(1), SECOND(2); private final int value; private Complex(int value) { this.value = value; } public int getValue() { return value; } }",
			expected: "enum Complex\n\tFIRST(1)\n\tSECOND(2)\n\tpublic int getValue()\n---\n",
		},
		{
			name: "nested enum in class",
			code: `
public class Container {
    public enum Status {
        ACTIVE, INACTIVE;
        public boolean isActive() { return this == ACTIVE; }
    }
    private enum Hidden { ONE, TWO }
}`,
			expected: `public class Container
---
	public enum Status
		ACTIVE
		INACTIVE
		public boolean isActive()
---
`,
		},
		{
			name:     "annotated enum",
			code:     "@Deprecated enum Legacy { OLD, OLDER }",
			expected: "@Deprecated enum Legacy\n\tOLD\n\tOLDER\n---\n",
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
		{
			name: "interface declaration",
			code: `
public interface Drawable {
    void draw();
    void resize();
}`,
			expected: "Drawable",
		},
		{
			name: "annotation declaration",
			code: `
public @interface TestAnnotation {
    String value() default "";
    int count() default 0;
}`,
			expected: "TestAnnotation",
		},
		{
			name: "nested class",
			code: `
public class Outer {
    private int x;
    
    public class Inner {
        public void innerMethod() {
            System.out.println("Inner method");
        }
    }
    
    public void outerMethod() {
        System.out.println("Outer method");
    }
}`,
			expected: "Outer, outerMethod, Inner, innerMethod",
		},
		{
			name: "multiple classes in single file",
			code: `
class First {
    public void firstMethod() {}
}

class Second {
    public void secondMethod() {}
}

class Third {
    private void thirdMethod() {}
}`,
			expected: "First, Second, Third, firstMethod, secondMethod",
		},
		{
			name: "class inheritance",
			code: `
public class Animal {
    public void makeSound() {}
}

public class Dog extends Animal {
    public void bark() {}
    private void sleep() {}
}`,
			expected: "Animal, Dog, makeSound, bark",
		},
		{
			name: "basic enum",
			code: `
public enum Direction {
    NORTH, SOUTH, EAST, WEST
}`,
			expected: "Direction",
		},
		{
			name: "enum with methods",
			code: `
public enum Operation {
    PLUS, MINUS, TIMES, DIVIDE;
    
    public double apply(double x, double y) {
        return 0.0;
    }
    
    private void helper() {
        // Helper method
    }
}`,
			expected: "Operation, apply",
		},
		{
			name: "nested enum",
			code: `
public class Container {
    public enum Status {
        ACTIVE, INACTIVE, PENDING;
        
        public boolean isTerminal() {
            return false;
        }
    }
    
    public void process() {
        // Process method
    }
}`,
			expected: "Container, Status, process, isTerminal",
		},
		{
			name: "multiple enums",
			code: `
enum Color {
    RED, GREEN, BLUE;
    public String getHex() { return ""; }
}

enum Size {
    SMALL, MEDIUM, LARGE;
    public int getValue() { return 0; }
    private void internal() {}
}`,
			expected: "Color, Size, getValue, getHex",
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

func TestGetSymbolDefinitionJava(t *testing.T) {
	testCases := []struct {
		name               string
		symbolName         string
		code               string
		expectedDefinition string
		expectedError      string
	}{
		{
			name:          "empty code",
			symbolName:    "TestClass",
			code:          "",
			expectedError: `symbol not found: TestClass`,
		},
		{
			name:       "basic class definition",
			symbolName: "TestClass",
			code: `public class TestClass {
    private String name;
}`,
			expectedDefinition: `public class TestClass {
    private String name;
}`,
		},
		{
			name:       "class with method",
			symbolName: "TestClass",
			code: `public class TestClass {
    public void testMethod() {
        System.out.println("Hello");
    }
}`,
			expectedDefinition: `public class TestClass {
    public void testMethod() {
        System.out.println("Hello");
    }
}`,
		},
		{
			name:       "method definition",
			symbolName: "testMethod",
			code: `public class TestClass {
    public void testMethod() {
        System.out.println("Hello");
    }
}`,
			expectedDefinition: `    public void testMethod() {
        System.out.println("Hello");
    }`,
		},
		{
			name:          "symbol not found",
			symbolName:    "NonExistentSymbol",
			code:          "public class SomeClass {}",
			expectedError: `symbol not found: NonExistentSymbol`,
		},
		{
			name:       "interface definition",
			symbolName: "TestInterface",
			code: `public interface TestInterface {
    void testMethod();
    String getName();
}`,
			expectedDefinition: `public interface TestInterface {
    void testMethod();
    String getName();
}`,
		},
		{
			name:       "annotation definition",
			symbolName: "TestAnnotation",
			code: `@interface TestAnnotation {
    String value() default "";
}`,
			expectedDefinition: `@interface TestAnnotation {
    String value() default "";
}`,
		},
		{
			name:       "nested class",
			symbolName: "InnerClass",
			code: `public class OuterClass {
    private static class InnerClass {
        private String field;
    }
}`,
			expectedDefinition: `    private static class InnerClass {
        private String field;
    }`,
		},
		{
			name:       "field definition",
			symbolName: "field",
			code: `public class TestClass {
    private static final String field = "test";
}`,
			expectedDefinition: `    private static final String field = "test";`,
		},
		{
			name:       "annotated class",
			symbolName: "AnnotatedClass",
			code: `@Deprecated
@SuppressWarnings("unchecked")
public class AnnotatedClass {
    private String name;
}`,
			expectedDefinition: `@Deprecated
@SuppressWarnings("unchecked")
public class AnnotatedClass {
    private String name;
}`,
		},
		{
			name:       "annotated method",
			symbolName: "annotatedMethod",
			code: `public class TestClass {
    @Override
    @Deprecated
    public void annotatedMethod() {
        System.out.println("test");
    }
}`,
			expectedDefinition: `    @Override
    @Deprecated
    public void annotatedMethod() {
        System.out.println("test");
    }`,
		},
		{
			name:       "enum definition",
			symbolName: "TestEnum",
			code: `public enum TestEnum {
    ONE,
    TWO,
    THREE
}`,
			expectedDefinition: `public enum TestEnum {
    ONE,
    TWO,
    THREE
}`,
		},
		{
			name:       "enum member definition",
			symbolName: "TWO",
			code: `public enum TestEnum {
    ONE,
    TWO,
    THREE
}`,
			expectedDefinition: `public enum TestEnum {
    ONE,
    TWO,
    THREE
}`,
		},
		{
			name:       "class with single line doc comments",
			symbolName: "DocClass",
			code: `// This is a documented class
// with multiple line comments
public class DocClass {
    private String name;
}`,
			expectedDefinition: `// This is a documented class
// with multiple line comments
public class DocClass {
    private String name;
}`,
		},
		{
			name:       "class with multi-line doc comments",
			symbolName: "DocClass2",
			code: `/* This is a documented class
 * with multiple lines
 * in a block comment
 */
public class DocClass2 {
    private String name;
}`,
			expectedDefinition: `/* This is a documented class
 * with multiple lines
 * in a block comment
 */
public class DocClass2 {
    private String name;
}`,
		},
		{
			name:       "method with single line doc comments",
			symbolName: "docMethod",
			code: `public class TestClass {
    // This method does something
    // really important
    public void docMethod() {
        System.out.println("Hello");
    }
}`,
			expectedDefinition: `    // This method does something
    // really important
    public void docMethod() {
        System.out.println("Hello");
    }`,
		},
		{
			name:       "method with multi-line doc comments",
			symbolName: "docMethod2",
			code: `public class TestClass {
    /* This method does something
     * really important
     * in a very special way
     */
    public void docMethod2() {
        System.out.println("Hello");
    }
}`,
			expectedDefinition: `    /* This method does something
     * really important
     * in a very special way
     */
    public void docMethod2() {
        System.out.println("Hello");
    }`,
		},
		{
			name:       "interface with doc comments",
			symbolName: "DocInterface",
			code: `// This interface defines
// a contract for something
public interface DocInterface {
    void testMethod();
}`,
			expectedDefinition: `// This interface defines
// a contract for something
public interface DocInterface {
    void testMethod();
}`,
		},
		{
			name:       "field with doc comments",
			symbolName: "docField",
			code: `public class TestClass {
    /* This field stores
     * an important value
     */
    private static final String docField = "test";
}`,
			expectedDefinition: `    /* This field stores
     * an important value
     */
    private static final String docField = "test";`,
		},
		{
			name:       "enum with doc comments",
			symbolName: "DocEnum",
			code: `// This enum represents
// some important values
public enum DocEnum {
    ONE,
    TWO,
    THREE
}`,
			expectedDefinition: `// This enum represents
// some important values
public enum DocEnum {
    ONE,
    TWO,
    THREE
}`,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			filePath, err := utils.WriteTestTempFile(t, "java", tc.code)
			if err != nil {
				t.Fatalf("Failed to write temp file: %v", err)
			}
			defer os.Remove(filePath)

			definition, err := GetSymbolDefinitionsString(filePath, tc.symbolName, 0)
			if err != nil {
				if tc.expectedError == "" {
					t.Fatalf("Unexpected error: %v", err)
				} else if !strings.Contains(err.Error(), tc.expectedError) {
					t.Fatalf("Expected error: %s, got: %v", tc.expectedError, err)
				}
			}

			if strings.TrimSuffix(definition, "\n") != strings.TrimSuffix(tc.expectedDefinition, "\n") {
				t.Errorf("Expected definition:\n%s\nGot:\n%s", utils.PanicJSON(tc.expectedDefinition), utils.PanicJSON(definition))
				t.Errorf("Expected definition:\n%s\nGot:\n%s", tc.expectedDefinition, definition)
			}
		})
	}
}
