//! Helpers the generated types use to read and write Go's JSON.
use serde::{Deserialize, Deserializer};

/// Reads null as the type's default: Go writes a nil slice or map as null.
pub fn nullable<'de, D, T>(d: D) -> Result<T, D::Error>
where
    D: Deserializer<'de>,
    T: Default + Deserialize<'de>,
{
    Ok(Option::<T>::deserialize(d)?.unwrap_or_default())
}

/// What Go's omitempty leaves out.
pub trait Empty {
    fn empty(&self) -> bool;
}

pub fn is_empty<T: Empty>(v: &T) -> bool {
    v.empty()
}

impl Empty for String {
    fn empty(&self) -> bool {
        self.is_empty()
    }
}
impl Empty for bool {
    fn empty(&self) -> bool {
        !*self
    }
}
impl Empty for i64 {
    fn empty(&self) -> bool {
        *self == 0
    }
}
impl Empty for u16 {
    fn empty(&self) -> bool {
        *self == 0
    }
}
impl Empty for f64 {
    fn empty(&self) -> bool {
        *self == 0.0
    }
}
impl Empty for serde_json::Value {
    fn empty(&self) -> bool {
        self.is_null()
    }
}
impl<T> Empty for Vec<T> {
    fn empty(&self) -> bool {
        self.is_empty()
    }
}
impl<K, V> Empty for std::collections::BTreeMap<K, V> {
    fn empty(&self) -> bool {
        self.is_empty()
    }
}
