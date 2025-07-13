(
  (decorator)* @class.decorator
  .
  (class_definition
    name: (identifier) @class.name
    superclasses: (_)? @class.superclasses
    body: (_
      . (expression_statement
        . (string) @class.docstring
      )?

      [
        (function_definition
          name: (identifier) @class.method.name
          parameters: (parameters) @class.method.parameters
          return_type: (type)? @class.method.return_type
          body: (_
            . (expression_statement
              . (string) @class.method.docstring
            )?
          ) @class.method.body
        ) @class.method.declaration

        (decorated_definition
          (decorator)+ @class.method.decorator
          definition: (function_definition
            name: (identifier) @class.method.name
            parameters: (parameters) @class.method.parameters
            return_type: (type)? @class.method.return_type
            body: (_
              . (expression_statement
                . (string) @class.method.docstring
              )?
            ) @class.method.body
          )

        ) @class.method.declaration

        (_)
      ]*

    ) @class.body
  ) @class.declaration
)

; extract method names as separate matches for symbol outline
(class_definition
  body: (_
    (function_definition
      name: (identifier) @method.name
    )
  )
)

(type_alias_statement
  . (type (identifier) @type.name)
) @type.declaration

(
  (module
    (expression_statement
      (assignment
        left: (identifier) @assignment.name
        type: (type)? @assignment.type
        right: [
          (call
            function: (identifier) @call_name
            ;(#eq? @call_name "NewType")
          )? @assignment.right
        ]
      )
    )
  )
)

(module
  (function_definition
    name: (identifier) @function.name
    parameters: (parameters) @function.parameters
    return_type: (type)? @function.return_type
    body: (_
      . (expression_statement
        . (string) @function.docstring
      )?
    ) @function.body
  ) @function.declaration
)

(module
  (decorated_definition
    (decorator)+ @function.decorator
    definition: (function_definition
      name: (identifier) @function.name
      parameters: (parameters) @function.parameters
      return_type: (type)? @function.return_type
      body: (_
        . (expression_statement
          . (string) @function.docstring
        )?
      ) @function.body
    ) @function.declaration
  )
)