(source_file
  (const_declaration
    (const_spec
      name: (identifier) @const.name
     )
  ) @const.declaration
)

(source_file
  (var_declaration
    (var_spec
      name: (identifier) @var.name
      type: (_)? @var.type
    )
  ) @var.declaration
)

(
  (comment)* @function.doc
  .
  (function_declaration
    name: (identifier) @function.name
    parameters: (parameter_list) @function.parameters
    result: (_)? @function.result
  ) @function.declaration
  (#strip! @function.doc "^//\\s*")
  (#select-adjacent! @function.doc @function.declaration)
)

(
  (comment)* @method.doc
  .
  (method_declaration
    receiver: (parameter_list
      (parameter_declaration
      	type: (_) @method.receiver_type
      )
    ) @method.receiver
    name: (field_identifier) @method.name
    parameters: (parameter_list) @method.parameters
    result: (_)? @method.result
  ) @method.declaration
  (#strip! @method.doc "^//\\s*")
  (#select-adjacent! @method.doc @method.declaration)
)

(
  (comment)* @type.doc
  .
  (type_declaration
    (_
      name: (type_identifier) @type.name
    )
  ) @type.declaration
  (#strip! @type.doc "^//\\s*")
  (#select-adjacent! @type.doc @type.declaration)
)