## Protobuf Style Guide

Use buf to generate golang and Typescript protobuf bindings.  Generated outputs go in the directories defined in docs/layout.md.  .gitignore rules should be added to prevent generated files from being checked into source control.

Use protobuf version 3.

Floating point numerical values should use the double type.