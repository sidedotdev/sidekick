(class_declaration
  (identifier) @class.name
  (class_body)? @class.body) @class.declaration

(method_declaration
  (identifier) @method.name
  (formal_parameters) @method.parameters
) @method.declaration

(constructor_declaration
  (identifier) @constructor.name
  (formal_parameters) @constructor.parameters) @constructor.declaration

(field_declaration
  (variable_declarator
    (identifier) @field.name)) @field.declaration