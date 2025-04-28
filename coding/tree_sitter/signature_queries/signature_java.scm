; Queries for top-level declarations (non-private/protected)

(class_declaration
  (modifiers)? @class.modifiers
    (#not-match? @class.modifiers "private|protected")
  name: (identifier) @class.name
  (type_parameters)? @class.type_parameters
  ; body: (_) @class.body ; Body not needed for signature capture itself
) @class.declaration

(interface_declaration
  (modifiers)? @interface.modifiers
    (#not-match? @interface.modifiers "private|protected")
  name: (identifier) @interface.name
  (type_parameters)? @interface.type_parameters
  ; body: (_) @interface.body
) @interface.declaration

(enum_declaration
  (modifiers)? @enum.modifiers
    (#not-match? @enum.modifiers "private|protected")
  name: (identifier) @enum.name
  ; body: (_) @enum.body
) @enum.declaration

(annotation_type_declaration
  (modifiers)? @annotation.modifiers
    (#not-match? @annotation.modifiers "private|protected")
  name: (identifier) @annotation.name
  ; body: (_) @annotation.body
) @annotation.declaration


; XXX NOTE: OLD QUERIES BELOW and commented out. these older queries are good
; for referencing how to do these queries, but are not to be uncommented EVER.
; Instead, add new queries above this line.
; 
;    (interface_declaration
;      (modifiers)? @interface.modifiers
;        (#not-match? @interface.modifiers "private|protected")
;      (identifier) @interface.name
;      (type_parameters)? @interface.type_parameters
;        body: (_
;          [
;            (method_declaration) @interface.method.declaration
;            (constant_declaration) @interface.constant.declaration
;            (field_declaration) @interface.field.declaration
;            (_)
;          ]*
;        ) @interface.body
;    ) @interface.declaration
;    
;    (annotation_type_declaration
;      (modifiers)? @annotation.modifiers
;        (#not-match? @annotation.modifiers "private|protected")
;      (identifier) @annotation.name
;        body: (_
;          [
;            (annotation_type_element_declaration
;              (modifiers)? @annotation.element.modifiers
;                (#not-match? @annotation.element.modifiers "private|protected")
;              type: (_) @annotation.element.type
;              (identifier) @annotation.element.name
;            ) @annotation.element.declaration
;    
;            (annotation_type_element_declaration
;              (modifiers)? @annotation.element.ignore.modifiers
;                (#match? @annotation.element.ignore.modifiers "private|protected")
;            )
;    
;            (constant_declaration) @annotation.constant.declaration
;    
;            (_)
;          ]*
;        ) @annotation.body
;    ) @annotation.declaration
;    
;    (class_declaration
;      (modifiers)? @class.modifiers
;        (#not-match? @class.modifiers "private|protected")
;      (identifier) @class.name
;      (type_parameters)? @class.type_parameters
;        body: (_
;          [
;            (method_declaration
;              (modifiers)? @class.method.modifiers
;                (#not-match? @class.method.modifiers "private|protected")
;              (type_parameters)? @class.method.type_parameters
;              type: (_) @class.method.type
;              (identifier) @class.method.name
;              (formal_parameters) @class.method.parameters
;            ) @class.method.declaration
;    
;            (method_declaration
;              (modifiers)? @class.method.ignored.modifiers
;                (#match? @class.method.ignored.modifiers "private|protected")
;            )
;    
;            (constructor_declaration
;              (modifiers)? @class.constructor.modifiers
;                (#not-match? @class.constructor.modifiers "private|protected")
;              (identifier) @class.constructor.name
;              (formal_parameters) @class.constructor.parameters
;            ) @class.constructor.declaration
;    
;            (constructor_declaration
;              (modifiers)? @class.constructor.ignored.modifiers
;                (#match? @class.constructor.ignored.modifiers "private|protected")
;            )
;    
;            (field_declaration
;              (modifiers)? @class.field.modifiers
;                (#not-match? @class.field.modifiers "private|protected")
;              (variable_declarator
;                (identifier) @class.field.name
;              )
;            ) @class.field.declaration
;    
;            (field_declaration
;              (modifiers)? @class.field.ignored.modifiers
;                (#match? @class.field.ignored.modifiers "private|protected")
;            ) @class.field.ignored.declaration
;    
;            (constant_declaration
;              (modifiers)? @class.constant.modifiers
;                (#not-match? @class.constant.modifiers "private|protected")
;              (variable_declarator
;                (identifier) @class.constant.name
;              )
;            ) @class.constant.declaration
;    
;            (constant_declaration
;              (modifiers)? @class.constant.ignored.modifiers
;                (#match? @class.constant.ignored.modifiers "private|protected")
;            ) @class.constant.ignored.declaration
;    
;            (_)
;          ]*
;        ) @class.body
;    ) @class.declaration
;    
;    (enum_declaration
;      (modifiers)? @enum.modifiers
;        (#not-match? @enum.modifiers "private|protected")
;      (identifier) @enum.name
;      body: (_
;        ; bug in tree-sitter means * isn't working here, so hard-coding many with ? instead
;        (enum_constant)? @enum.constant.declaration
;        (enum_constant)? @enum.constant.declaration
;        (enum_constant)? @enum.constant.declaration
;        (enum_constant)? @enum.constant.declaration
;        (enum_constant)? @enum.constant.declaration
;        (enum_constant)? @enum.constant.declaration
;        (enum_constant)? @enum.constant.declaration
;        (enum_constant)? @enum.constant.declaration
;        (enum_constant)? @enum.constant.declaration
;        (enum_constant)? @enum.constant.declaration
;        (enum_constant)? @enum.constant.declaration
;        (enum_constant)? @enum.constant.declaration
;        (enum_constant)? @enum.constant.declaration
;        (enum_constant)? @enum.constant.declaration
;        (enum_constant)? @enum.constant.declaration
;        (enum_constant)? @enum.constant.declaration
;        (enum_body_declarations
;          [
;            (method_declaration
;              (modifiers)? @enum.method.modifiers
;                (#not-match? @enum.method.modifiers "private|protected")
;              type: (_) @enum.method.type
;              (identifier) @enum.method.name
;              (formal_parameters) @enum.method.parameters
;            ) @enum.method.declaration
;    
;            (method_declaration
;              (modifiers)? @enum.method.ignored.modifiers
;                (#match? @enum.method.ignored.modifiers "private|protected")
;            )
;    
;            (field_declaration
;              (modifiers)? @enum.field.modifiers
;                (#not-match? @enum.field.modifiers "private|protected")
;              (variable_declarator
;                (identifier) @enum.field.name
;              )
;            ) @enum.field.declaration
;    
;            (field_declaration
;              (modifiers)? @enum.field.ignored.modifiers
;                (#match? @enum.field.ignored.modifiers "private|protected")
;            ) @enum.field.ignored.declaration
;    
;            (_)
;          ]*
;        )?
;      ) @enum.body
;    ) @enum.declaration
;    
;    ; extract method names as separate matches for symbol outline
;    (class_declaration
;      body: (_
;        (method_declaration
;          (modifiers)? @method.modifiers
;            (#not-match? @method.modifiers "private|protected")
;          (identifier) @method.name
;        )
;      )
;    )
;    
;    ; extract enum method names as separate matches for symbol outline
;    (enum_declaration
;      (enum_body
;        (enum_body_declarations
;          (method_declaration
;            (modifiers)? @method.modifiers
;              (#not-match? @method.modifiers "private|protected")
;            (identifier) @method.name
;          )
;        )
;      )
;    )