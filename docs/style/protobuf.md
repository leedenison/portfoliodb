## Protobuf Style Guide

Use buf and protoc to generate golang and Typescript protobuf bindings.  Generated outputs should be placed in server/gen and client/gen target directories.  .gitignore rules should be added to prevent these from being checked into source control.

Use protobuf version 3.

Floating point numerical values should use the double type.