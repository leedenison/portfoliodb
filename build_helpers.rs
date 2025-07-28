use prost_types::{DescriptorProto, FileDescriptorSet};

/// Given a descriptor set, finds all message fields whose Protobuf type matches `target_type`
/// and applies `attribute` to each field using `config.field_attribute(...)`.
///
/// # Arguments
///
/// - `config`: the tonic-build config
/// - `descriptor_set`: parsed prost_types::FileDescriptorSet
/// - `target_type`: fully-qualified Protobuf type (e.g., ".my.package.MyType")
/// - `attribute`: the Rust attribute to apply (e.g., "#[serde(default)]")
pub fn apply_field_attribute_for_type(
    config: tonic_build::Builder,
    descriptor_set: &FileDescriptorSet,
    target_type: &str,
    attribute: &str,
) -> tonic_build::Builder {
    let mut config = config;
    for file in &descriptor_set.file {
        let package = file.package();
        for message in &file.message_type {
            config = collect_fields_recursive(config, package, "", message, target_type, attribute);
        }
    }
    config
}

/// Given a descriptor set, finds all message fields whose Protobuf type matches any of the `target_types`
/// and applies `attribute` to each field using `config.field_attribute(...)`.
///
/// # Arguments
///
/// - `config`: the tonic-build config
/// - `descriptor_set`: parsed prost_types::FileDescriptorSet
/// - `target_types`: vector of fully-qualified Protobuf types (e.g., [".my.package.MyType", ".my.package.AnotherType"])
/// - `attribute`: the Rust attribute to apply (e.g., "#[serde(default)]")
pub fn apply_field_attribute_for_types(
    config: tonic_build::Builder,
    descriptor_set: &FileDescriptorSet,
    target_types: &[&str],
    attribute: &str,
) -> tonic_build::Builder {
    let mut config = config;
    for target_type in target_types {
        config = apply_field_attribute_for_type(config, descriptor_set, target_type, attribute);
    }
    config
}

fn collect_fields_recursive(
    mut config: tonic_build::Builder,
    package: &str,
    parent_path: &str,
    message: &DescriptorProto,
    target_type: &str,
    attribute: &str,
) -> tonic_build::Builder {
    let message_name = message.name();
    let full_message_path = if parent_path.is_empty() {
        format!("{}", message_name)
    } else {
        format!("{}.{}", parent_path, message_name)
    };

    // Check if this message is the target type
    let current_message_type = if package.is_empty() {
        format!(".{}", full_message_path)
    } else {
        format!(".{}.{}", package, full_message_path)
    };

    // If this is the target message type, apply attribute to all its fields
    if current_message_type == target_type {
        for field in &message.field {
            let field_name = field.name();
            let fq_field_path = if package.is_empty() {
                format!("{}.{}", full_message_path, field_name)
            } else {
                format!("{}.{}.{}", package, full_message_path, field_name)
            };
            config = config.field_attribute(&fq_field_path, attribute);
        }
    }

    // Recurse into nested types
    for nested in &message.nested_type {
        config = collect_fields_recursive(
            config,
            package,
            &full_message_path,
            nested,
            target_type,
            attribute,
        );
    }
    config
}
