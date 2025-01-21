(interface_declaration
  (modifiers)? @interface.modifiers
  (identifier) @interface.name
    body: (_
      [
        (method_declaration) @interface.method.declaration
        (constant_declaration) @interface.constant.declaration
        (field_declaration) @interface.field.declaration
        (_)
      ]*
    ) @interface.body
) @interface.declaration

(class_declaration
  (identifier) @class.name
    body: (_
      [
        (method_declaration
          (modifiers)? @class.method.modifiers
            (#not-match? @class.method.modifiers "private|protected")
          type: (_) @class.method.type
          (identifier) @class.method.name
          (formal_parameters) @class.method.parameters
        ) @class.method.declaration

        (method_declaration
          (modifiers)? @class.method.ignored.modifiers
            (#match? @class.method.ignored.modifiers "private|protected")
        )

        (constructor_declaration
          (modifiers)? @class.method.modifiers
            (#not-match? @class.method.modifiers "private|protected")
          (identifier) @class.constructor.name
          (formal_parameters) @class.constructor.parameters
        ) @class.constructor.declaration

        (constructor_declaration
          (modifiers)? @class.constructor.ignored.modifiers
            (#match? @class.constructor.ignored.modifiers "private|protected")
        )

        (field_declaration
          (modifiers)? @class.field.modifiers
            (#not-match? @class.field.modifiers "private|protected")
          (variable_declarator
            (identifier) @class.field.name
          )
        ) @class.field.declaration

        (field_declaration
          (modifiers)? @class.field.ignored.modifiers
            (#match? @class.field.ignored.modifiers "private|protected")
        ) @class.field.ignored.declaration

        (constant_declaration
          (modifiers)? @class.constant.modifiers
            (#not-match? @class.constant.modifiers "private|protected")
          (variable_declarator
            (identifier) @class.constant.name
          )
        ) @class.constant.declaration

        (constant_declaration
          (modifiers)? @class.constant.ignored.modifiers
            (#match? @class.constant.ignored.modifiers "private|protected")
        ) @class.constant.ignored.declaration

        (_)
      ]*
    ) @class.body
) @class.declaration

; extract method names as separate matches for symbol outline
(class_declaration
  body: (_
    (method_declaration
      (modifiers)? @method.modifiers
        (#not-match? @method.modifiers "private|protected")
      (identifier) @method.name
    )
  )
)