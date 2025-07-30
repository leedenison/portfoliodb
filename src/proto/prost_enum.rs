#[macro_export]
macro_rules! prost_enum {
    ($mod_name:ident, $enum_type:path) => {
        pub mod $mod_name {
            use serde::de::{self, Visitor};
            use serde::{Deserializer, Serializer};
            use std::convert::TryFrom;

            pub fn serialize<S>(value: &i32, serializer: S) -> Result<S::Ok, S::Error>
            where
                S: Serializer,
            {
                let enum_type = <$enum_type>::try_from(*value).ok();
                match enum_type {
                    Some(e) => serializer.serialize_str(e.as_str_name()),
                    None => serializer.serialize_i32(*value),
                }
            }

            pub fn deserialize<'de, D>(deserializer: D) -> Result<i32, D::Error>
            where
                D: Deserializer<'de>,
            {
                struct EnumVisitor;

                impl<'de> Visitor<'de> for EnumVisitor {
                    type Value = i32;

                    fn expecting(&self, f: &mut std::fmt::Formatter) -> std::fmt::Result {
                        write!(f, "valid {} string or integer", stringify!($enum_type))
                    }

                    fn visit_str<E: de::Error>(self, v: &str) -> Result<Self::Value, E> {
                        <$enum_type>::from_str_name(v)
                            .map(|e| e as i32)
                            .ok_or_else(|| {
                                E::invalid_value(de::Unexpected::Str(v), &"valid enum string")
                            })
                    }

                    fn visit_i64<E: de::Error>(self, v: i64) -> Result<Self::Value, E> {
                        Ok(v as i32)
                    }

                    fn visit_u64<E: de::Error>(self, v: u64) -> Result<Self::Value, E> {
                        Ok(v as i32)
                    }
                }

                deserializer.deserialize_any(EnumVisitor)
            }

            /// Converts an i32 value to the enum and returns its string name.
            /// Returns "UNKNOWN" if the i32 value doesn't correspond to a valid enum variant.
            pub fn from_i32(value: i32) -> String {
                <$enum_type>::try_from(value)
                    .map(|e| e.as_str_name().to_string())
                    .unwrap_or_else(|_| "UNKNOWN".to_string())
            }
        }
    };
}
