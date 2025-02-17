; Capture non-private/protected classes for signatures and symbols
(class_declaration
  (modifiers)? @class.modifiers
    (#not-match? @class.modifiers "private|protected")
  (type_identifier) @class.name
  (type_parameters)? @class.type_parameters
  (primary_constructor)? @class.primary_constructor
  (class_body
    [
      (function_declaration
        (modifiers)? @class.method.modifiers
          (#not-match? @class.method.modifiers "private|protected")
        (type_parameters)? @class.method.type_parameters
        (simple_identifier) @class.method.name
        (function_value_parameters) @class.method.parameters
        (user_type)? @class.method.return_type
      ) @class.method.declaration

      ; private/protected matching alternate to make sure overall class capture
      ; still happens
      (function_declaration
        (modifiers)? @class.method.ignored.modifiers
          (#match? @class.method.ignored.modifiers "private|protected")
      )

      (property_declaration
        (modifiers)? @class.method.modifiers
          (#not-match? @class.method.modifiers "private|protected")
        (variable_declaration
          (simple_identifier) @class.property.name
        )
      ) @class.property.declaration

      ; private/protected matching alternate to make sure overall class capture
      ; still happens
      (property_declaration
        (modifiers)? @class.property.ignored.modifiers
          (#match? @class.property.ignored.modifiers "private|protected")
      )

      (_)
    ]*
  )? @class.body

  (enum_class_body
    [
      (function_declaration
        (modifiers)? @class.method.modifiers
          (#not-match? @class.method.modifiers "private|protected")
        (type_parameters)? @class.method.type_parameters
        (simple_identifier) @class.method.name
        (function_value_parameters) @class.method.parameters
        (user_type)? @class.method.return_type
      ) @class.method.declaration

      ; private/protected matching alternate to make sure overall class capture
      ; still happens
      (function_declaration
        (modifiers)? @class.method.ignored.modifiers
          (#match? @class.method.ignored.modifiers "private|protected")
      )
      
      (enum_entry
        (simple_identifier) @class.enum_entry.name
      ) @class.enum_entry.declaration
      
      ","
      ";"
      (_)
    ]*
  )? @class.enum_body

) @class.declaration

; Top-level function declarations
(source_file
  (function_declaration
    (modifiers)? @function.modifiers
      ; not sure if we wanna exclude internal too here
      (#not-match? @function.modifiers "private")
    (type_parameters)? @function.type_parameters
    (simple_identifier) @function.name
    (function_value_parameters) @function.parameters
    (user_type)? @function.return_type
  ) @function.declaration
)

; Extract method names for symbol outline
(class_declaration
  (modifiers)? @method.class.modifiers
    (#not-match? @method.class.modifiers "private|protected")
  (_
    (function_declaration
      (modifiers)? @method.modifiers
        (#not-match? @method.modifiers "private|protected")
      (simple_identifier) @method.name
    )
  )
)

; Extract enum entry names for symbol outline
(class_declaration
  (modifiers)? @enum_entry.class.modifiers
    (#not-match? @enum_entry.class.modifiers "private|protected")
  (_
    (enum_entry
      (simple_identifier) @enum_entry.name
    )
  )
)