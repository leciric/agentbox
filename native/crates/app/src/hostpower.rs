//! The computer's power, for the window's presence lease (app/presence.rs),
//! as the Electron app's main process reads it (desktop/src/main/hostpower.ts):
//! whether it runs on battery, and on macOS how hot it is. The daemon backs
//! its housekeeping off while it says save (api.HostPower.Saving).
//!
//! Electron's powerMonitor tells it apart on each system; here each system is
//! asked directly, with no library: on Linux the kernel's power supplies in
//! sysfs (what UPower reads), on macOS IOKit's providing power source and
//! NSProcessInfo's thermal state, on Windows GetSystemPowerStatus. Elsewhere,
//! or when it can't be read, it is on mains and "" (fine), as Electron's is.
use agentbox_api::HostPower;

/// read is the power now. It is cheap (a few small files, or one system
/// call), so the lease reads it again every few seconds instead of waiting
/// for the system to say it changed.
pub fn read() -> HostPower {
    HostPower {
        on_battery: on_battery(),
        thermal: thermal(),
    }
}

#[cfg(target_os = "linux")]
fn on_battery() -> bool {
    linux::on_battery(std::path::Path::new("/sys/class/power_supply"))
}

#[cfg(target_os = "linux")]
fn thermal() -> String {
    String::new()
}

#[cfg(target_os = "linux")]
mod linux {
    use std::path::Path;

    /// on_battery is no mains supply online, and a battery discharging: a
    /// desktop with no battery, a laptop on its charger, or a battery that is
    /// full and idle on the charger is on mains. A machine whose supplies
    /// can't be read (a VM, a container) is on mains.
    pub(super) fn on_battery(dir: &Path) -> bool {
        let Ok(entries) = std::fs::read_dir(dir) else {
            return false;
        };
        let read = |p: &Path, f: &str| {
            std::fs::read_to_string(p.join(f))
                .map(|s| s.trim().to_string())
                .unwrap_or_default()
        };
        let mut discharging = false;
        for e in entries.flatten() {
            let p = e.path();
            match read(&p, "type").as_str() {
                "Mains" | "USB" if read(&p, "online") == "1" => return false,
                "Battery" if read(&p, "scope") != "Device" => {
                    // A wireless mouse's battery (scope Device) says nothing
                    // of the computer's.
                    discharging |= read(&p, "status") == "Discharging";
                }
                _ => {}
            }
        }
        discharging
    }

    #[cfg(test)]
    mod tests {
        use super::on_battery;
        use std::path::Path;

        fn supply(dir: &Path, name: &str, files: &[(&str, &str)]) {
            let p = dir.join(name);
            std::fs::create_dir_all(&p).unwrap();
            for (f, v) in files {
                std::fs::write(p.join(f), format!("{v}\n")).unwrap();
            }
        }

        #[test]
        fn reads_the_supplies() {
            let base = std::env::temp_dir().join(format!("hostpower-{}", std::process::id()));
            let _ = std::fs::remove_dir_all(&base);
            // None at all: a desktop, a VM.
            assert!(!on_battery(&base.join("missing")));

            let laptop = base.join("laptop");
            supply(&laptop, "AC", &[("type", "Mains"), ("online", "0")]);
            supply(
                &laptop,
                "BAT0",
                &[("type", "Battery"), ("status", "Discharging")],
            );
            assert!(on_battery(&laptop));
            supply(&laptop, "AC", &[("type", "Mains"), ("online", "1")]);
            assert!(!on_battery(&laptop));

            // On its charger, full: not discharging.
            let full = base.join("full");
            supply(&full, "BAT0", &[("type", "Battery"), ("status", "Full")]);
            assert!(!on_battery(&full));

            // A mouse's battery running down says nothing of the computer.
            let mouse = base.join("mouse");
            supply(
                &mouse,
                "hidpp_battery_0",
                &[
                    ("type", "Battery"),
                    ("scope", "Device"),
                    ("status", "Discharging"),
                ],
            );
            assert!(!on_battery(&mouse));
            let _ = std::fs::remove_dir_all(&base);
        }
    }
}

#[cfg(target_os = "macos")]
fn on_battery() -> bool {
    mac::on_battery()
}

#[cfg(target_os = "macos")]
fn thermal() -> String {
    mac::thermal()
}

#[cfg(target_os = "macos")]
mod mac {
    use std::ffi::{c_char, c_void};

    type CFTypeRef = *const c_void;

    #[link(name = "IOKit", kind = "framework")]
    unsafe extern "C" {
        fn IOPSCopyPowerSourcesInfo() -> CFTypeRef;
        fn IOPSGetProvidingPowerSourceType(snapshot: CFTypeRef) -> CFTypeRef;
    }

    #[link(name = "CoreFoundation", kind = "framework")]
    unsafe extern "C" {
        fn CFRelease(cf: CFTypeRef);
        fn CFStringGetCString(s: CFTypeRef, buf: *mut c_char, len: isize, encoding: u32) -> u8;
    }

    #[link(name = "objc")]
    unsafe extern "C" {
        fn objc_getClass(name: *const c_char) -> *mut c_void;
        fn sel_registerName(name: *const c_char) -> *mut c_void;
        fn objc_msgSend();
    }

    #[link(name = "Foundation", kind = "framework")]
    unsafe extern "C" {}

    const UTF8: u32 = 0x0800_0100;

    /// on_battery is IOKit's providing power source being the battery
    /// ("Battery Power", kIOPMBatteryPowerKey), what powerMonitor asks.
    pub(super) fn on_battery() -> bool {
        unsafe {
            let info = IOPSCopyPowerSourcesInfo();
            if info.is_null() {
                return false;
            }
            // Not owned: it lives as long as info.
            let kind = IOPSGetProvidingPowerSourceType(info);
            let mut buf = [0 as c_char; 64];
            let ok = !kind.is_null()
                && CFStringGetCString(kind, buf.as_mut_ptr(), buf.len() as isize, UTF8) != 0;
            CFRelease(info);
            ok && std::ffi::CStr::from_ptr(buf.as_ptr()).to_bytes() == b"Battery Power"
        }
    }

    /// thermal is [NSProcessInfo processInfo].thermalState, named as
    /// powerMonitor.getCurrentThermalState names it.
    pub(super) fn thermal() -> String {
        unsafe {
            let class = objc_getClass(c"NSProcessInfo".as_ptr());
            if class.is_null() {
                return String::new();
            }
            let send_id: unsafe extern "C" fn(*mut c_void, *mut c_void) -> *mut c_void =
                std::mem::transmute(objc_msgSend as unsafe extern "C" fn());
            let info = send_id(class, sel_registerName(c"processInfo".as_ptr()));
            if info.is_null() {
                return String::new();
            }
            let send_int: unsafe extern "C" fn(*mut c_void, *mut c_void) -> isize =
                std::mem::transmute(objc_msgSend as unsafe extern "C" fn());
            let state = send_int(info, sel_registerName(c"thermalState".as_ptr()));
            super::thermal_name(state).to_string()
        }
    }
}

/// thermal_name is NSProcessInfoThermalState's name, as Electron's.
#[cfg_attr(not(target_os = "macos"), allow(dead_code))]
fn thermal_name(state: isize) -> &'static str {
    match state {
        0 => "nominal",
        1 => "fair",
        2 => "serious",
        3 => "critical",
        _ => "",
    }
}

#[cfg(windows)]
fn on_battery() -> bool {
    #[repr(C)]
    #[derive(Default)]
    struct SystemPowerStatus {
        ac_line_status: u8,
        battery_flag: u8,
        battery_life_percent: u8,
        system_status_flag: u8,
        battery_life_time: u32,
        battery_full_life_time: u32,
    }
    #[link(name = "kernel32")]
    unsafe extern "system" {
        fn GetSystemPowerStatus(status: *mut SystemPowerStatus) -> i32;
    }
    let mut s = SystemPowerStatus::default();
    // ACLineStatus 0 is offline, 1 online, 255 unknown (on mains, here).
    unsafe { GetSystemPowerStatus(&mut s) != 0 && s.ac_line_status == 0 }
}

#[cfg(windows)]
fn thermal() -> String {
    String::new()
}

#[cfg(not(any(target_os = "linux", target_os = "macos", windows)))]
fn on_battery() -> bool {
    false
}

#[cfg(not(any(target_os = "linux", target_os = "macos", windows)))]
fn thermal() -> String {
    String::new()
}

#[cfg(test)]
mod tests {
    #[test]
    fn names_the_thermal_states_as_electron() {
        let names: Vec<_> = (0..5).map(super::thermal_name).collect();
        assert_eq!(names, ["nominal", "fair", "serious", "critical", ""]);
    }
}
