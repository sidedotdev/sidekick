package tree_sitter

import (
	"os"
	"strings"
	"testing"

	"sidekick/utils"

	"github.com/stretchr/testify/assert"
)

func TestGetFileSignaturesStringKotlin(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name     string
		input    string
		expected string
	}{
		{
			name: "top level function",
			input: `fun main() {
    println("Hello, World!")
}`,
			expected: `fun main()
---
`,
		},
		{
			name: "generic function",
			input: `fun <T> genericFunc(item: T): T {
    return item
}`,
			expected: `fun <T> genericFunc(item: T): T
---
`,
		},
		{
			name: "function with parameters",
			input: `fun greet(name: String, age: Int) {
    println("Hello, $name! You are $age years old.")
}`,
			expected: `fun greet(name: String, age: Int)
---
`,
		},
		{
			name: "function with return type",
			input: `fun calculate(x: Int, y: Int): Int {
    return x + y
}`,
			expected: `fun calculate(x: Int, y: Int): Int
---
`,
		},
		{
			name: "annotated function",
			input: `@Deprecated("Use newMethod instead")
fun oldMethod() {
    println("old")
}`,
			// ideally we'd retain the newline, but it's not important
			expected: `@Deprecated("Use newMethod instead") fun oldMethod()
---
`,
		},
		{
			name: "private function excluded",
			input: `private fun internal() {
    println("internal")
}`,
			expected: ``,
		},
		{
			name: "simple classes",
			input: `
class Empty
class Empty2 {}
`,
			expected: `class Empty
---
class Empty2
---
`,
		},
		{
			name: "class with methods",
			input: `
class Hello {
  fun a() {}
  private fun hidden() {}
  fun b() {}
}
`,
			expected: `class Hello
---
	fun a()
---
	fun b()
---
`,
		},
		{
			name: "class with generic method",
			input: `class Container<T> {
    fun <R> transform(item: T, mapper: (T) -> R): R {
        return mapper(item)
    }
}`,
			expected: `class Container<T>
---
	fun <R> transform(item: T, mapper: (T) -> R): R
---
`,
		},
		{
			name: "generic class",
			input: `
class Container<T> {}
`,
			expected: `class Container<T>
---
`,
		},
		{
			name: "class with properties",
			input: `
class Something {
  val x: Int = 4
  var y: Int?
}
`,
			expected: `class Something
---
	val x: Int = 4
---
	var y: Int?
---
`,
		},
		{
			name: "enum class",
			input: `
enum class Color(val rgb: Int) {
  RED(0xFF0000),
  GREEN(0x00FF00)
}
`,
			expected: `enum class Color(val rgb: Int)
	RED
	GREEN
---
`,
		},
		{
			name: "value class",
			input: `
@JvmInline
value class Password(private val s: String)
`,
			expected: `@JvmInline
value class Password(private val s: String)
---
`,
		},
		{
			name: "private class and methods",
			input: `
private class Hidden {
  fun alsoHidden() {}
  private fun invisible() {}
}

class Visible {
  private fun hidden() {}
  protected fun alsoHidden() {}
  fun shown() {}
  fun alsoShown() {}
}
`,
			expected: `class Visible
---
	fun shown()
---
	fun alsoShown()
---
`,
		},
		{
			name: "class with mixed properties",
			input: `
class MixedProps {
   val a: Int = 1
   private val b: Int = 2
   protected var c: String? = null
   var d: Boolean = false
}
`,
			expected: `class MixedProps
---
	val a: Int = 1
---
	var d: Boolean = false
---
`,
		},

		{
			name: "protected class",
			input: `
protected class Protected {
  fun someMethod() {}
}
`,
			expected: ``,
		},
		{
			name: "enum class with mixed methods",
			input: `
enum class MixedMethods {
  X, Y;
  fun a() {}
  private fun hidden() {}
}
`,
			expected: `enum class MixedMethods
	X
	Y
---
	fun a()
---
`,
		},
		{
			name: "class with single annotation",
			input: `
@Test
class Empty
`,
			expected: `@Test class Empty
---
`,
		},
		{
			name: "class with annotated parameter",
			input: `
class Empty(@field:Test val x: Boolean)
`,
			expected: `class Empty(@field:Test val x: Boolean)
---
`,
		},
		{
			name: "property with multiple annotations in set",
			input: `
class Container {
    @set:[Inject VisibleForTesting]
    var x: Int = 0
}
`,
			expected: `class Container
---
	@set:[Inject VisibleForTesting]
    var x: Int = 0
---
`,
		},
		{
			name: "property with multiple separate annotations",
			input: `
class X {
    @A @B
    override val s: String = ""
}
`,
			expected: `class X
---
	@A @B
    override val s: String = ""
---
`,
		},
		{
			name: "function with multiple annotations",
			input: `
class X {
    @A @B
    fun s(): String { return "" }
}
`,
			expected: `class X
---
	@A @B fun s(): String
---
`,
		},
		{
			name: "top level annotated function",
			input: `
@Test
fun foo() = bar {}
`,
			expected: `@Test fun foo()
---
`,
		},
		{
			name: "class with private property, public method and public property",
			input: `
class ResourceManager {
    private val resources = mutableListOf<Resource>()

    fun registerResource(resource: Resource) {
        resources.add(resource)
    }

    val allResources: Collection<Resource>
        get() = resources
}
`,
			expected: `class ResourceManager
---
	fun registerResource(resource: Resource)
---
	val allResources: Collection<Resource>
---
`,
		},
		{
			name: "object with private property, public method and public property",
			input: `
object ResourceManager {
    private val resources = mutableListOf<Resource>()

    fun registerResource(resource: Resource) {
        resources.add(resource)
    }

    val allResources: Collection<Resource>
        get() = resources
}
`,
			expected: `object ResourceManager
---
	fun registerResource(resource: Resource)
---
	val allResources: Collection<Resource>
---
`,
		},
		{
			name: "top level properties",
			input: `
val greeting: String = "Hello"
var counter: Int = 0
private val secret: String = "hidden"
const val MAX_COUNT = 100
@Deprecated("Use newGreeting instead")
var oldGreeting = "Hi"
`,
			expected: `val greeting: String = "Hello"
---
var counter: Int = 0
---
const val MAX_COUNT = 100
---
@Deprecated("Use newGreeting instead")
var oldGreeting = "Hi"
---
`,
		},
		{
			name: "top level properties with type inference",
			input: `
val inferred = 42
var mutable = "changeable"
private val hidden = true
`,
			expected: `val inferred = 42
---
var mutable = "changeable"
---
`,
		},
		{
			name: "top level properties with annotations",
			input: `@JvmField
val javaField = "java"
@Volatile
var sharedVar = 0
@Deprecated("Old") @JvmStatic
val oldValue = ""
`,
			expected: `@JvmField
val javaField = "java"
---
@Volatile
var sharedVar = 0
---
@Deprecated("Old") @JvmStatic
val oldValue = ""
---
`,
		},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			// Create a temporary file with the test case code
			tmpfile, err := os.CreateTemp("", "test*.kt")
			if err != nil {
				t.Fatalf("Failed to create temp file: %v", err)
			}
			defer os.Remove(tmpfile.Name())

			if _, err := tmpfile.Write([]byte(tt.input)); err != nil {
				t.Fatalf("Failed to write to temp file: %v", err)
			}
			if err := tmpfile.Close(); err != nil {
				t.Fatalf("Failed to close temp file: %v", err)
			}

			// Call GetFileSignatures with the path to the temp file
			result, err := GetFileSignaturesString(tmpfile.Name())
			assert.NoError(t, err)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestGetFileSymbolsStringKotlin(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name     string
		code     string
		expected string
	}{
		{
			name: "top level function",
			code: `fun main() {
    println("Hello, World!")
}`,
			expected: `main`,
		},
		{
			name: "generic function",
			code: `fun <T> genericFunc(item: T): T {
    return item
}`,
			expected: `genericFunc`,
		},
		{
			name: "private function excluded",
			code: `private fun internal() {
    println("internal")
}`,
			expected: ``,
		},
		{
			name: "class with generic method",
			code: `class Container<T> {
    fun <R> transform(item: T, mapper: (T) -> R): R {
        return mapper(item)
    }
}`,
			expected: `Container, transform`,
		},
		{
			name: "simple classes",
			code: `
class Empty
class Empty2 {}
`,
			expected: "Empty, Empty2",
		},
		{
			name: "class with methods",
			code: `
class Hello {
  fun a() {}
  private fun hidden() {}
  fun b() {}
}
`,
			expected: "Hello, a, b",
		},
		{
			name: "generic class",
			code: `
class Container<T> {}
`,
			expected: "Container",
		},
		{
			name: "class with properties",
			code: `
class Something {
  val x: Int = 4
  var y: Int?
}
`,
			expected: "Something",
		},
		{
			name: "enum class",
			code: `
enum class Color(val rgb: Int) {
  RED(0xFF0000),
  GREEN(0x00FF00)
}
`,
			expected: "Color, RED, GREEN",
		},
		{
			name: "value class",
			code: `
@JvmInline
value class Password(private val s: String)
`,
			expected: "Password",
		},
		{
			name: "private class and methods",
			code: `
private class Hidden {
  fun visible() {}
  private fun invisible() {}
}

class Visible {
  private fun hidden() {}
  protected fun alsoHidden() {}
  fun shown() {}
  fun alsoShown() {}
}
`,
			expected: "Visible, shown, alsoShown",
		},
		{
			name: "class with mixed properties",
			code: `
class MixedProps {
   val a: Int = 1
   private val b: Int = 2
   protected var c: String? = null
   var d: Boolean = false
}
`,
			expected: "MixedProps",
		},

		{
			name: "protected class",
			code: `
protected class Protected {
  fun someMethod() {}
}
`,
			expected: "",
		},
		{
			name: "class with private properties",
			code: `
class MixedProps {
  val publicProp: String = "hello"
  private val secret: String = "secret"
  protected var alsoSecret: Int = 100
  var another: Boolean = true
}
`,
			expected: "MixedProps",
		},
		{
			name: "class with private property, public method and public property",
			code: `
class ResourceManager {
    private val resources = mutableListOf<Resource>()

    fun registerResource(resource: Resource) {
        resources.add(resource)
    }

    val allResources: Collection<Resource>
        get() = resources
}
`,
			expected: "ResourceManager, registerResource",
		},
		{
			name: "object with private property, public method and public property",
			code: `
object ResourceManager {
    private val resources = mutableListOf<Resource>()

    fun registerResource(resource: Resource) {
        resources.add(resource)
    }

    val allResources: Collection<Resource>
        get() = resources
}
`,
			expected: "ResourceManager, registerResource",
		},
		{
			name: "top level properties",
			code: `
val greeting: String = "Hello"
var counter: Int = 0
private val secret: String = "hidden"
const val MAX_COUNT = 100
@Deprecated("Use newGreeting instead")
var oldGreeting = "Hi"
`,
			expected: "greeting, counter, MAX_COUNT, oldGreeting",
		},
		{
			name: "top level properties with type inference",
			code: `
val inferred = 42
var mutable = "changeable"
private val hidden = true
`,
			expected: "inferred, mutable",
		},
		{
			name: "top level properties with annotations",
			code: `
@JvmField
val javaField = "java"
@Volatile
var sharedVar = 0
@Deprecated("Old") @JvmStatic
val oldValue = ""
`,
			expected: "javaField, sharedVar, oldValue",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			tmpfile, err := os.CreateTemp("", "*.kt")
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

func TestGetSymbolDefinitionKotlin(t *testing.T) {
	t.Parallel()
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
			code: `class TestClass {
    private val name: String = ""
}`,
			expectedDefinition: `class TestClass {
    private val name: String = ""
}`,
		},
		{
			name:       "class with method",
			symbolName: "TestClass",
			code: `class TestClass {
    fun testMethod() {
        println("Hello")
    }
}`,
			expectedDefinition: `class TestClass {
    fun testMethod() {
        println("Hello")
    }
}`,
		},
		{
			name:       "method definition",
			symbolName: "testMethod",
			code: `class TestClass {
    fun testMethod() {
        println("Hello")
    }
}`,
			expectedDefinition: `    fun testMethod() {
        println("Hello")
    }`,
		},
		{
			name:          "symbol not found",
			symbolName:    "NonExistentSymbol",
			code:          "class SomeClass {}",
			expectedError: `symbol not found: NonExistentSymbol`,
		},
		{
			name:       "enum class definition",
			symbolName: "TestEnum",
			code: `enum class TestEnum {
    ONE,
    TWO(2),
    THREE
}`,
			expectedDefinition: `enum class TestEnum {
    ONE,
    TWO(2),
    THREE
}`,
		},
		{
			name:       "enum entry definition",
			symbolName: "TWO",
			code: `enum class TestEnum {
    ONE,
    TWO(2),
    THREE
}`,
			expectedDefinition: `    TWO(2),`,
		},
		{
			name:       "class with single line doc comments",
			symbolName: "DocClass",
			code: `// This is a documented class
// with multiple line comments
class DocClass {
    private val name: String = ""
}`,
			expectedDefinition: `// This is a documented class
// with multiple line comments
class DocClass {
    private val name: String = ""
}`,
		},
		{
			name:       "class with multi-line doc comments",
			symbolName: "DocClass2",
			code: `/* This is a documented class
 * with multiple lines
 * in a block comment
 */
class DocClass2 {
    private val name: String = ""
}`,
			expectedDefinition: `/* This is a documented class
 * with multiple lines
 * in a block comment
 */
class DocClass2 {
    private val name: String = ""
}`,
		},
		{
			name:       "method with single line doc comments",
			symbolName: "docMethod",
			code: `class TestClass {
    // This method does something
    // really important
    fun docMethod() {
        println("Hello")
    }
}`,
			expectedDefinition: `    // This method does something
    // really important
    fun docMethod() {
        println("Hello")
    }`,
		},
		{
			name:       "method with multi-line doc comments",
			symbolName: "docMethod2",
			code: `class TestClass {
    /* This method does something
     * really important
     * in a very special way
     */
    fun docMethod2() {
        println("Hello")
    }
}`,
			expectedDefinition: `    /* This method does something
     * really important
     * in a very special way
     */
    fun docMethod2() {
        println("Hello")
    }`,
		},
		{
			name:       "generic class definition",
			symbolName: "Container",
			code: `class Container<T> {
    private var value: T? = null
    
    fun setValue(item: T) {
        value = item
    }
    
    fun getValue(): T? = value
}`,
			expectedDefinition: `class Container<T> {
    private var value: T? = null
    
    fun setValue(item: T) {
        value = item
    }
    
    fun getValue(): T? = value
}`,
		},
		{
			name:       "generic function definition",
			symbolName: "transform",
			code: `fun <T, R> transform(input: T, mapper: (T) -> R): R {
    return mapper(input)
}`,
			expectedDefinition: `fun <T, R> transform(input: T, mapper: (T) -> R): R {
    return mapper(input)
}`,
		},
		{
			name:       "class with generic method",
			symbolName: "convertTo",
			code: `class TypeConverter {
    fun <T> convertTo(value: Any): T {
        return value as T
    }
}`,
			expectedDefinition: `    fun <T> convertTo(value: Any): T {
        return value as T
    }`,
		},
		{
			name:       "value class definition",
			symbolName: "Password",
			code: `@JvmInline
value class Password(private val value: String) {
    fun masked(): String = "*".repeat(value.length)
}`,
			expectedDefinition: `@JvmInline
value class Password(private val value: String) {
    fun masked(): String = "*".repeat(value.length)
}`,
		},
		{
			name:       "backtick quoted identifier",
			symbolName: "`is data`",
			code: `class TestClass {
    // Simple backtick-quoted function name
    fun ` + "`is data`" + `(): Boolean {
        return true
    }
}`,
			expectedDefinition: `    // Simple backtick-quoted function name
    fun ` + "`is data`" + `(): Boolean {
        return true
    }`,
		},
		{
			name:       "backtick quoted identifier with special characters",
			symbolName: "`is-valid?`",
			code: `class TestClass {
    // Function name with special characters
    fun ` + "`is-valid?`" + `(): Boolean {
        return true
    }
}`,
			expectedDefinition: `    // Function name with special characters
    fun ` + "`is-valid?`" + `(): Boolean {
        return true
    }`,
		},
		{
			name:       "backtick quoted identifier with unicode",
			symbolName: "`π`",
			code: `class TestClass {
    // Unicode symbol in function name
    fun ` + "`π`" + `(): Double {
        return 3.14159
    }
}`,
			expectedDefinition: `    // Unicode symbol in function name
    fun ` + "`π`" + `(): Double {
        return 3.14159
    }`,
		},
		{
			name:       "object definition",
			symbolName: "Singleton",
			code: `object Singleton {
    private var count = 0
    
    fun increment() {
        count++
    }
    
    fun getCount(): Int = count
}`,
			expectedDefinition: `object Singleton {
    private var count = 0
    
    fun increment() {
        count++
    }
    
    fun getCount(): Int = count
}`,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			filePath, err := utils.WriteTestTempFile(t, "kt", tc.code)
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

func TestGetFileHeadersStringKotlin(t *testing.T) {
	t.Parallel()
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
			code:     "import java.util.Scanner",
			expected: "import java.util.Scanner\n",
		},
		{
			name:     "multiple imports",
			code:     "import java.util.Scanner\nimport kotlin.collections.List",
			expected: "import java.util.Scanner\nimport kotlin.collections.List\n",
		},
		{
			name:     "wildcard import",
			code:     "import kotlin.collections.*",
			expected: "import kotlin.collections.*\n",
		},
		{
			name:     "package declaration",
			code:     "package com.example.app",
			expected: "package com.example.app\n",
		},
		{
			name:     "simple file annotation",
			code:     "@file:JvmMultifileClass",
			expected: "@file:JvmMultifileClass\n",
		},
		{
			name:     "file annotation with arguments",
			code:     "@file:JvmName(\"BuildersKt\")",
			expected: "@file:JvmName(\"BuildersKt\")\n",
		},
		{
			name:     "multiple file annotations",
			code:     "@file:JvmMultifileClass\n@file:JvmName(\"BuildersKt\")",
			expected: "@file:JvmMultifileClass\n@file:JvmName(\"BuildersKt\")\n",
		},
		{
			name:     "package + import",
			code:     "package com.example.app\nimport kotlin.collections.List",
			expected: "package com.example.app\nimport kotlin.collections.List\n",
		},
		{
			name:     "file annotation + package + import",
			code:     "@file:JvmMultifileClass\npackage com.example.app\nimport kotlin.collections.List",
			expected: "@file:JvmMultifileClass\npackage com.example.app\nimport kotlin.collections.List\n",
		},
		{
			name:     "package + empty line + import",
			code:     "package com.example.app\n\nimport kotlin.collections.List",
			expected: "package com.example.app\n\nimport kotlin.collections.List\n",
		},
		{
			name:     "package + multiple whitespace lines + import",
			code:     "package com.example.app\n\n\t\t\n  \n \t \t\nimport kotlin.collections.List",
			expected: "package com.example.app\n\n\t\t\n  \n \t \t\nimport kotlin.collections.List\n",
		},
		{
			name:     "package later in file",
			code:     "import kotlin.collections.List\npackage com.example.app",
			expected: "import kotlin.collections.List\n",
		},
		{
			name:     "import later in file",
			code:     "package com.example.app\nclass Main {}\nimport kotlin.collections.List",
			expected: "package com.example.app\n",
		},
		{
			name:     "file annotation later in file",
			code:     "package com.example.app\nclass Main {}\n@file:JvmName(\"BuildersKt\")",
			expected: "package com.example.app\n",
		},
		{
			name:     "package twice in file",
			code:     "package com.example.app\nclass Main {}\npackage com.other.app",
			expected: "package com.example.app\n",
		},
		{
			name:     "import twice in file",
			code:     "import kotlin.collections.List\nclass Main {}\nimport kotlin.collections.Set",
			expected: "import kotlin.collections.List\n",
		},
		{
			name:     "complex_combination",
			code:     "@file:JvmMultifileClass\n@file:JvmName(\"BuildersKt\")\npackage com.example.app\n\nimport kotlin.collections.List\nimport kotlin.collections.*\nclass Main {}\n@file:Suppress(\"unused\")\npackage com.other.app\nimport kotlin.collections.Set",
			expected: "@file:JvmMultifileClass\n@file:JvmName(\"BuildersKt\")\npackage com.example.app\n\nimport kotlin.collections.List\nimport kotlin.collections.*\n",
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			tmpfile, err := os.CreateTemp("", "*.kt")
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

func TestGetAllAlternativeFileSymbolsKotlin(t *testing.T) {
	t.Parallel()
	// Define the test cases
	testCases := []struct {
		name           string
		input          string
		expectedOutput []string
	}{
		{
			name: "Function with backticks",
			input: `
				fun ` + "`" + `test-function` + "`" + `() {
				}
			`,
			expectedOutput: []string{"`test-function`", "test-function"},
		},
		{
			name: "Property with backticks",
			input: `
				val ` + "`" + `special-property` + "`" + ` = 42
			`,
			expectedOutput: []string{"`special-property`", "special-property"},
		},
		{
			name: "Function without backticks",
			input: `
				fun normalFunction() {
				}
			`,
			expectedOutput: []string{"normalFunction"},
		},
		{
			name: "Property without backticks",
			input: `
				val normalProperty = 42
			`,
			expectedOutput: []string{"normalProperty"},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			// Write the input to a temp file with a '.kt' extension
			filePath, err := utils.WriteTestTempFile(t, "kt", tc.input)
			if err != nil {
				t.Fatalf("Failed to create temp file: %v", err)
			}
			defer os.Remove(filePath)

			// Call the function and check the output
			output, err := GetAllAlternativeFileSymbols(filePath)
			if err != nil {
				t.Fatalf("failed to get all alternative file symbols: %v", err)
			}

			// Convert output to string slice for comparison
			outputStr := symbolToStringSlice(output)
			if !assert.ElementsMatch(t, outputStr, tc.expectedOutput) {
				t.Errorf("Expected %s, but got %s", utils.PanicJSON(tc.expectedOutput), utils.PanicJSON(outputStr))
			}

			// Verify properties of alternative symbols
			for _, symbol := range output {
				if strings.HasPrefix(symbol.SymbolType, "alt_") {
					// Find the original symbol
					var originalSymbol Symbol
					for _, s := range output {
						if !strings.HasPrefix(s.SymbolType, "alt_") &&
							strings.TrimPrefix(symbol.SymbolType, "alt_") == s.SymbolType {
							originalSymbol = s
							break
						}
					}
					// Alternative should have same points as original
					assert.Equal(t, originalSymbol.StartPoint, symbol.StartPoint)
					assert.Equal(t, originalSymbol.EndPoint, symbol.EndPoint)
				}
			}
		})
	}
}

func TestShrinkKotlinEmbeddedCodeContext(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name         string
		code         string
		expected     string
		expectShrink bool
	}{
		{
			name: "preserves private and protected members",
			code: `package test

private class PrivateClass {
    private val secret = "not hidden"
    protected fun protectedMethod() {}
    internal fun internalMethod() {}
    fun publicMethod() {}
}

class PublicClass {
    private companion object {
        const val PRIVATE_CONST = "secret"
    }
    
    protected class ProtectedNested
    private object PrivateObject
    
    private var privateVar = 0
    protected val protectedVal = ""
    internal var internalVar = false
    var publicVar = true
}`,
			// FIXME want these lines too but doesn't work yet:
			/*
				private companion object
					const val PRIVATE_CONST
			*/
			expected: `Shrank context - here are the extracted code signatures and docstrings only, in lieu of full code:
` + "```" + `kotlin-signatures
private class PrivateClass
	private val secret = "not hidden"
	protected fun protectedMethod()
	internal fun internalMethod()
	fun publicMethod()
class PublicClass
	protected class ProtectedNested
	private object PrivateObject
	private var privateVar = 0
	protected val protectedVal = ""
	internal var internalVar = false
	var publicVar = true
` + "```",
			expectShrink: true,
		},
		{
			name: "preserves private enum entries",
			code: `package test

enum class Visibility {
    PUBLIC,
    PRIVATE,
    PROTECTED;

    private fun hiddenMethod() {}
    protected val protectedProp = ""
}`,
			expected: `Shrank context - here are the extracted code signatures and docstrings only, in lieu of full code:
` + "```" + `kotlin-signatures
enum class Visibility
	PUBLIC
	PRIVATE
	PROTECTED
	private fun hiddenMethod()
	protected val protectedProp = ""
` + "```",
			expectShrink: true,
		},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			input := createMarkdownCodeBlock("kotlin", tt.code)
			result, didShrink := ShrinkEmbeddedCodeContext(input, false, len(tt.code)-100)

			// Normalize line endings for comparison
			normalizedCode := strings.TrimSpace(strings.ReplaceAll(tt.code, "\r\n", "\n"))
			normalizedResult := strings.TrimSpace(strings.ReplaceAll(result, "\r\n", "\n"))
			normalizedExpected := strings.TrimSpace(strings.ReplaceAll(tt.expected, "\r\n", "\n"))

			if normalizedCode == normalizedResult {
				assert.False(t, didShrink)
			} else {
				assert.True(t, didShrink)
			}
			if normalizedResult != normalizedExpected {
				t.Errorf("ShrinkEmbeddedCodeContext() got:\n%s\n\nwant:\n%s", normalizedResult, normalizedExpected)
				t.Errorf("ShrinkEmbeddedCodeContext() got:\n%s\n\nwant:\n%s", utils.PrettyJSON(normalizedResult), utils.PrettyJSON(normalizedExpected))
			}
			if didShrink != tt.expectShrink {
				t.Errorf("ShrinkEmbeddedCodeContext() didShrink = %v, want %v", didShrink, tt.expectShrink)
			}
		})
	}
}

func TestNormalizeSymbolFromSnippet_Kotlin(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name     string
		snippet  string
		expected string
	}{
		{
			name:     "Method or Top-level function signature",
			snippet:  "fun top(a: String): String",
			expected: "top",
		},
		{
			name:     "Class signature",
			snippet:  "class K",
			expected: "K",
		},
	}

	for _, tc := range tests {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got, err := NormalizeSymbolFromSnippet("kotlin", tc.snippet)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got != tc.expected {
				t.Fatalf("expected %q, got %q", tc.expected, got)
			}
		})
	}
}
