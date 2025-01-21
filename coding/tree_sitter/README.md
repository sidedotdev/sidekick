# Tree-sitter Symbol Processing

This package provides two related but distinct symbol processing capabilities:

## Symbol Listing (GetFileSymbolsString)

Symbol listing provides a curated subset of symbols that are most relevant for navigation and high-level code understanding. This is exposed through `GetFileSymbolsString` and related functions.

The symbol listing:
- Returns a carefully selected subset of symbols
- Focuses on public/exported symbols by default
- Prioritizes symbols that are most relevant for navigation
- Excludes implementation details like private members
- Is optimized for code outline and navigation use cases

## Symbol Definitions (GetSymbolDefinitionsString)

Symbol definitions provide comprehensive symbol lookup capability, capturing ALL possible symbols that could be referenced in the code. This is exposed through `GetSymbolDefinitionsString` and related functions.

The symbol definitions:
- Capture every possible symbol that could be referenced
- Include private/internal symbols
- Include full symbol context (modifiers, annotations, etc)
- Capture nested and local symbols
- Support precise code analysis and refactoring tools

## Relationship Between Listings and Definitions

Symbol listings and definitions serve complementary purposes and work together to provide a complete code understanding and navigation experience:

### Technical Relationship

1. **Shared Foundation**: Both capabilities use Tree-sitter queries but with different purposes:
   - Symbol definitions use comprehensive queries (*.scm.mustache files) that capture every possible symbol
   - Symbol listings use more selective queries that focus on navigational relevance

2. **Complementary Processing**:
   - Symbol definitions provide the complete universe of symbols and their full context
   - Symbol listings filter and process this information to present a cleaner interface

### Use Cases and Integration

1. **Code Navigation**:
   ```go
   // First: Use symbol listing for high-level navigation
   symbols, _ := GetFileSymbolsString("MyClass.java")
   // Returns navigable structure:
   // - public class MyClass
   // - public interface DataProcessor
   // - public void processData()
   
   // Then: Use symbol definitions for detailed inspection
   def, _ := GetSymbolDefinitionsString("MyClass.java", "processData")
   // Returns complete implementation context:
   // @Override
   // public void processData(String input) {
   //     privateHelper(input);
   //     // ... entire method body
   // }
   ```

2. **Refactoring and Analysis**:
   ```go
   // Symbol listing identifies primary API surface
   symbols, _ := GetFileSymbolsString("Service.java")
   // Shows:
   // - public class Service
   // - public void apiMethod()
   
   // Symbol definitions find all related implementation details
   def, _ := GetSymbolDefinitionsString("Service.java", "privateHelper")
   // Finds internal implementations:
   // private void privateHelper(String input) {
   //     // implementation details needed for refactoring
   // }
   ```

### Practical Examples

1. **IDE-like Features**:
   - Quick navigation uses symbol listings for the outline view
   - "Go to Definition" uses symbol definitions for precise lookups
   - Refactoring tools use symbol definitions to find all occurrences

2. **Documentation Generation**:
   - API documentation uses symbol listings to show public interface
   - Internal documentation uses symbol definitions for complete coverage

3. **Code Analysis**:
   - Security scanning uses symbol definitions to analyze all code paths
   - Public API analysis uses symbol listings to focus on exposed interfaces

The symbol definition queries (in `symbol_definition_queries/*.scm.mustache`) are designed to be comprehensive, capturing every possible symbol that could be referenced in the code. This ensures that tools can look up any symbol's definition, regardless of its visibility or scope.

In contrast, the symbol listing functionality filters this comprehensive set down to the most relevant symbols for navigation and high-level understanding. This provides a cleaner, more focused view for code browsing and navigation use cases.

This separation of concerns allows each feature to be optimized for its specific use case while maintaining complete symbol coverage where needed. Tools can leverage both capabilities together - using listings for high-level navigation and definitions for detailed analysis.