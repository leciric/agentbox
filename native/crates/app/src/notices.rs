//! The system's side of the app's notices (desktop/src/main/notify.ts and
//! renderer/lib/reviewAlert.ts): a notification the OS shows, the chime, and
//! the count on the launcher's icon. What they say and when is app/alerts.rs's;
//! this only hands them to the platform.
//!
//! A notification is clicked on another thread (Linux's D-Bus server answers
//! there): its id goes into CLICKS, which the window's loop takes (take_clicks)
//! to bring itself forward and go where the notification points.
use std::sync::Mutex;
use std::time::Duration;

static CLICKS: Mutex<Vec<String>> = Mutex::new(Vec::new());

/// take_clicks is the notifications clicked since it was last asked.
pub fn take_clicks() -> Vec<String> {
    std::mem::take(&mut *CLICKS.lock().unwrap_or_else(|e| e.into_inner()))
}

/// show hands a notification to the OS, the click on it reported by its id:
/// Linux's notification servers as the "default" action, a Mac's and
/// Windows' as the default response, so each goes where it points.
pub fn show(id: String, title: String, body: String) {
    show_with(id, title, body, agentbox_i18n::t("shell.app.view"));
}

/// show_with is show with the action's name given (Linux's servers draw it
/// as a button).
pub fn show_with(id: String, title: String, body: String, action: String) {
    std::thread::spawn(move || {
        #[cfg(target_os = "macos")]
        set_mac_application();
        let mut n = notify_rust::Notification::new();
        n.appname("AgentBox").summary(&title).body(&body);
        #[cfg(target_os = "linux")]
        {
            n.action("default", &action);
            match n.show() {
                Ok(handle) => handle.wait_for_action(|action| {
                    if action == "default" {
                        CLICKS.lock().unwrap_or_else(|e| e.into_inner()).push(id);
                    }
                }),
                Err(e) => eprintln!("notification: {e}"),
            }
        }
        // A Mac's (NSUserNotificationCenter, its delegate answered on the
        // main run loop GPUI keeps turning) and Windows' toasts report a
        // click on the notification's body as its default response.
        #[cfg(not(target_os = "linux"))]
        {
            let _ = action;
            let clicked = move |r: &notify_rust::NotificationResponse| {
                if matches!(r, notify_rust::NotificationResponse::Default) {
                    CLICKS.lock().unwrap_or_else(|e| e.into_inner()).push(id);
                }
            };
            match n.show() {
                Ok(handle) => {
                    if let Err(e) = handle.wait_for_response(clicked) {
                        eprintln!("notification: {e}");
                    }
                }
                Err(e) => eprintln!("notification: {e}"),
            }
        }
    });
}

/// set_mac_application tells notify-rust which app notifications come from,
/// once, before the first one: left alone it asks Launch Services for
/// "use_default" and macOS shows a "Where is use_default?" picker.
#[cfg(target_os = "macos")]
fn set_mac_application() {
    static ONCE: std::sync::Once = std::sync::Once::new();
    ONCE.call_once(|| {
        if let Err(e) = notify_rust::set_application("dev.agentbox.native") {
            eprintln!("notification: {e}");
        }
    });
}

/// chime is playReadySound: E5 then A5, a rising fourth with a soft attack
/// and a bell-like decay, made here so there's no file to ship. volume is
/// 0–100, and loudness goes with its square, as Electron's gain does.
pub fn chime(volume: i64) {
    let level = volume.clamp(0, 100) as f32 / 100.;
    if level == 0. {
        return;
    }
    std::thread::spawn(move || {
        if let Err(e) = play(0.3 * level * level) {
            eprintln!("chime: {e}");
        }
    });
}

/// NOTES are the chime's: each frequency and when it starts, in seconds.
const NOTES: [(f32, f32); 2] = [(659.25, 0.02), (880., 0.15)];
const QUIET: f32 = 0.0001;

/// chime_sample is the chime's sound at t seconds, peak its loudest.
pub fn chime_sample(t: f32, peak: f32) -> f32 {
    let mut out = 0.;
    for (freq, at) in NOTES {
        let dt = t - at;
        if !(0. ..0.6).contains(&dt) {
            continue;
        }
        // WebAudio's exponential ramps: up to peak in 15 ms, down to quiet
        // by 0.55 s, stopped at 0.6 s.
        let gain = if dt < 0.015 {
            QUIET * (peak / QUIET).powf(dt / 0.015)
        } else if dt < 0.55 {
            peak * (QUIET / peak).powf((dt - 0.015) / 0.535)
        } else {
            QUIET
        };
        out += (std::f32::consts::TAU * freq * dt).sin() * gain;
    }
    out
}

fn play(peak: f32) -> anyhow::Result<()> {
    use anyhow::Context as _;
    use cpal::traits::{DeviceTrait as _, HostTrait as _, StreamTrait as _};
    let device = cpal::default_host()
        .default_output_device()
        .context("no audio output")?;
    let config = device.default_output_config()?;
    let rate = config.sample_rate().0 as f32;
    let channels = config.channels() as usize;
    let mut n = 0u64;
    let mut next = move || {
        let s = chime_sample(n as f32 / rate, peak);
        n += 1;
        s
    };
    let err = |e| eprintln!("chime: {e}");
    let stream = match config.sample_format() {
        cpal::SampleFormat::F32 => device.build_output_stream(
            &config.into(),
            move |out: &mut [f32], _: &_| {
                for frame in out.chunks_mut(channels) {
                    frame.fill(next());
                }
            },
            err,
            None,
        )?,
        cpal::SampleFormat::I16 => device.build_output_stream(
            &config.into(),
            move |out: &mut [i16], _: &_| {
                for frame in out.chunks_mut(channels) {
                    frame.fill((next() * i16::MAX as f32) as i16);
                }
            },
            err,
            None,
        )?,
        cpal::SampleFormat::U16 => device.build_output_stream(
            &config.into(),
            move |out: &mut [u16], _: &_| {
                for frame in out.chunks_mut(channels) {
                    frame.fill(((next() + 1.) * 0.5 * u16::MAX as f32) as u16);
                }
            },
            err,
            None,
        )?,
        f => anyhow::bail!("audio output takes {f:?}"),
    };
    stream.play()?;
    std::thread::sleep(Duration::from_millis(900));
    Ok(())
}

/// set_badge puts count, what waits on the user across the chats' sessions,
/// on the launcher's icon where the desktop reads Unity's LauncherEntry
/// (KDE, Ubuntu's dock, Plank), as Electron's app.setBadgeCount does; 0 takes
/// it off. GPUI has no dock badge for a Mac nor a taskbar count on Windows:
/// there app/alerts.rs asks for the window's attention when the count rises.
pub fn set_badge(count: i64) {
    #[cfg(target_os = "linux")]
    std::thread::spawn(move || {
        use std::collections::HashMap;
        use zbus::zvariant::Value;
        let Ok(conn) = zbus::blocking::Connection::session() else {
            return;
        };
        let mut props: HashMap<&str, Value> = HashMap::new();
        props.insert("count", Value::I64(count));
        props.insert("count-visible", Value::Bool(count > 0));
        let _ = conn.emit_signal(
            None::<zbus::names::BusName>,
            "/dev/agentbox/native",
            "com.canonical.Unity.LauncherEntry",
            "Update",
            &("application://agentbox-native.desktop", props),
        );
    });
    #[cfg(not(target_os = "linux"))]
    let _ = count;
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn the_chime_rises_and_fades() {
        // Silent before the first note, loudest just after each attack,
        // quiet again once both have rung out.
        assert_eq!(chime_sample(0.0, 0.3), 0.);
        let loud = (0..200)
            .map(|i| chime_sample(0.035 + i as f32 / 48_000., 0.3).abs())
            .fold(0., f32::max);
        assert!(loud > 0.2, "{loud}");
        assert!(chime_sample(0.7, 0.3).abs() < 0.001);
        assert_eq!(chime_sample(0.8, 0.3), 0.);
    }
}
