use prost::Message;
use prost_types::FileDescriptorSet;

mod build_helpers;
use crate::build_helpers::apply_field_attribute_for_types;

fn base_config() -> tonic_build::Builder {
    tonic_build::configure()
        .build_server(true)
        .compile_well_known_types(true)
        .protoc_arg("--experimental_allow_proto3_optional")
        .extern_path(".google.protobuf.Timestamp", "pbjson_types::Timestamp")
        .type_attribute(".", "#[derive(serde::Serialize, serde::Deserialize)]")
        .field_attribute(
            ".portfoliodb.Tx.tx_type",
            "#[serde(with = \"crate::prost_tx_type\")]",
        )
        .field_attribute(
            ".portfoliodb.Instrument.type",
            "#[serde(with = \"crate::prost_instrument_type\")]",
        )
        .field_attribute(
            ".portfoliodb.Option.put_call",
            "#[serde(with = \"crate::prost_put_call\")]",
        )
        .field_attribute(
            ".portfoliodb.Option.style",
            "#[serde(with = \"crate::prost_option_style\")]",
        )
        .field_attribute(
            ".portfoliodb.Error.code",
            "#[serde(with = \"crate::prost_error_code\")]",
        )
}

fn main() -> Result<(), Box<dyn std::error::Error>> {
    let out_dir = std::env::var("OUT_DIR")?;
    let descriptor_path = std::path::PathBuf::from(&out_dir).join("portfoliodb.bin");

    let proto_files = &["proto/service/portfoliodb.proto"];
    let include_dirs = &["proto"];

    // === 1. First pass: generate descriptor only ===
    base_config()
        .file_descriptor_set_path(&descriptor_path)
        .out_dir(&out_dir)
        .compile_protos(proto_files, include_dirs)?;

    // === 2. Load descriptor ===
    let descriptor_bytes = std::fs::read(&descriptor_path)?;
    let descriptor = FileDescriptorSet::decode(&*descriptor_bytes)?;

    // === 3. Second pass: apply #[serde(default)] to all fields of portfoliodb types ===
    let config = apply_field_attribute_for_types(
        base_config(),
        &descriptor,
        &[
            ".portfoliodb.Tx",
            ".portfoliodb.Price",
            ".portfoliodb.Instrument",
            ".portfoliodb.Identifier",
            ".portfoliodb.Derivative",
            ".portfoliodb.Option",
            ".portfoliodb.Broker",
        ],
        "#[serde(default)]",
    );

    config
        .file_descriptor_set_path(&descriptor_path)
        .out_dir(&out_dir)
        .compile_protos(proto_files, include_dirs)?;

    // === 4. Generate pbjson serde glue ===
    pbjson_build::Builder::new()
        .register_descriptors(&descriptor_bytes)?
        .build(&[".portfoliodb"])?;

    println!("cargo:rerun-if-changed=proto/service/portfoliodb.proto");

    Ok(())
}
