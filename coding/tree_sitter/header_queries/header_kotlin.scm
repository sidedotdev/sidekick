; Combined capture of package, file annotations, and imports in sequence
(
    (package_header) @header.package
    .
    (file_annotation)* @header.file_annotations
    .
    (import)+ @header.imports
  (#select-adjacent! @header.package @header.file_annotations)
  (#select-adjacent! @header.file_annotations @header.imports)
) @header

; Capture package and imports without file annotations
(
    (package_header) @header.package
    .
    (import)+ @header.imports
  (#select-adjacent! @header.package @header.imports)
) @header

; Standalone captures for individual elements
(package_header) @header
(file_annotation) @header
(import) @header