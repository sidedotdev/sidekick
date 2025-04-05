; Capture non-private/protected classes for signatures and symbols
(class_declaration
  (modifiers)? @class.modifiers
    (#not-match? @class.modifiers "private|protected")
  (type_identifier) @class.name
  (type_parameters)? @class.type_parameters
  (primary_constructor)? @class.primary_constructor

  (class_body
  )? @class.body

  (enum_class_body
    [
      (enum_entry
        (simple_identifier) @class.enum_entry.name
      ) @class.enum_entry.declaration
      
      _
    ]*
  )? @class.enum_body

) @class.declaration

; just like with classes, but for objects now: reusing the @class internals though, just want a different declaration
(object_declaration
  (modifiers)? @class.modifiers
    (#not-match? @class.modifiers "private|protected")
  (type_identifier) @class.name
  (type_parameters)? @class.type_parameters
  (class_body
  )? @class.body
) @object.declaration

; function declarations (top-level or within classes/objects)
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
(class_declaration
  (modifiers)? @parent.modifiers
    (#not-match? @parent.modifiers "private|protected")
    (_
      (function_declaration
        (modifiers)? @function.modifiers
          (#not-match? @function.modifiers "private|protected")
        (type_parameters)? @function.type_parameters
        (simple_identifier) @function.name
        (function_value_parameters) @function.parameters
        (user_type)? @function.return_type
      ) @function.declaration
    )
)
(object_declaration
  (modifiers)? @parent.modifiers
    (#not-match? @parent.modifiers "private|protected")
    (_
      (function_declaration
        (modifiers)? @function.modifiers
          (#not-match? @function.modifiers "private|protected")
        (type_parameters)? @function.type_parameters
        (simple_identifier) @function.name
        (function_value_parameters) @function.parameters
        (user_type)? @function.return_type
      ) @function.declaration
    )
)

; property declarations (top-level or within classes/objects)
(source_file
  (property_declaration
    (modifiers)? @property.modifiers
      ; not sure if we wanna exclude internal too here
      (#not-match? @property.modifiers "private")
    (variable_declaration
      (simple_identifier) @property.name
    )
  ) @property.declaration
)
(class_declaration
  (modifiers)? @parent.modifiers
    (#not-match? @parent.modifiers "private|protected")
    (_
      (property_declaration
        (modifiers)? @property.modifiers
          (#not-match? @property.modifiers "private|protected")
        (variable_declaration
          (simple_identifier) @property.ignored.name
        )
      ) @property.declaration
    )
)
(object_declaration
  (modifiers)? @parent.modifiers
    (#not-match? @parent.modifiers "private|protected")
    (_
      (property_declaration
        (modifiers)? @property.modifiers
          ; not sure if we wanna exclude internal too here
          (#not-match? @property.modifiers "private|protected")
        (variable_declaration
          (simple_identifier) @property.ignored.name
        )
      ) @property.declaration
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