(function_declaration
  name: (identifier) @function.name
  type_parameters: (_)? @function.type_parameters
  parameters: (formal_parameters) @function.parameters
  return_type: (_)? @function.return_type
) @function.declaration

(interface_declaration
  name: (_) @interface.name
) @interface.declaration

(type_alias_declaration
  name: (_) @type_alias.name
) @type_alias.declaration

(enum_declaration
  name: (identifier) @enum.name
) @enum.declaration

(enum_declaration
  body: (enum_body
    name: (_)? @enum_member.name
    (enum_assignment
      name: (_)? @enum_member.name
    )*
  )
)

; only top-level lexical declarations are included in signatures
(program
  (lexical_declaration
    (variable_declarator
      name: (identifier) @lexical.name
    )
  ) @lexical.declaration
)

; only top-level variable declarations are included in signatures
(program
  (variable_declaration
    (variable_declarator
      name: (identifier) @var.name
    )
  ) @var.declaration
)

; node type can be class_declaration or just class
(_
  name: (_) @class.name
  (class_heritage)* @class.heritage
  (class_body
    [
      (public_field_definition
        (accessibility_modifier)* @class.field.mod
        name: (_) @class.field.name
        type: (_)? @class.field.type
      ) @class.field.declaration

      (method_definition
        (accessibility_modifier)* @class.method.mod
        name: (_) @class.method.name
        parameters: (_) @class.method.parameters
        return_type: (_)? @class.method.return_type
        body: (_) @class.method.body
      ) @class.method.declaration

      (_)

      ";"
    ]*
  ) @class.body
) @class.declaration


; for symbol outline
(method_definition
  name: (_) @method.name
)
