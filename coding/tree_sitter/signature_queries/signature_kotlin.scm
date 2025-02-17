(class_declaration
  (modifiers)? @class.modifiers
  (type_identifier) @class.name
  (type_parameters)? @class.type_parameters
  (primary_constructor)? @class.primary_constructor
  (class_body
    [
      (function_declaration
        (simple_identifier) @class.method.name
        (function_value_parameters) @class.method.parameters
      ) @class.method.declaration

      (property_declaration
        (variable_declaration
          (simple_identifier) @class.property.name
        )
      ) @class.property.declaration

      (_)
    ]*
  )? @class.body

  (enum_class_body
    [
      (function_declaration
        (simple_identifier) @class.method.name
        (function_value_parameters) @class.method.parameters
      ) @class.method.declaration

      (property_declaration
        (variable_declaration
          (simple_identifier) @class.property.name
        )
      ) @class.property.declaration
      
      (enum_entry
        (simple_identifier) @class.enum_entry.name
      ) @class.enum_entry.declaration
      
      ","

      (_)
    ]*
  )? @class.enum_body
) @class.declaration