fn main() -> Result<(), Box<dyn std::error::Error>> {
    let out_dir = std::env::var("OUT_DIR").unwrap();
    tonic_build::configure()
        .build_client(true)
        .build_server(true)
        .out_dir(&out_dir)
        .protoc_arg("--experimental_allow_proto3_optional")
        .extern_path(".google.protobuf.Timestamp", "pbjson_types::Timestamp")
        .type_attribute(".", "#[derive(serde::Serialize, serde::Deserialize)]")
        .compile_protos(
            &["proto/service/portfoliodb.proto"],
            &["proto"], // Include paths (protoc -I argument)
        )?;

    println!("cargo:rerun-if-changed=proto/service/portfoliodb.proto");

    Ok(())
}
