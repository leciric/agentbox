# Remedy (Android fixture)

A tiny Android app for AgentBox's Android evidence: a list of medication reminders, with an empty state. It stores the list on the device, so each emulator has its own.

## Build

```sh
sudo apt-get install -y openjdk-21-jdk-headless   # once, if there's no JDK
./build.sh                                        # writes build/remedy.apk
```

`build.sh` uses the Android SDK's build tools directly (aapt2, d8, apksigner) instead of Gradle, so the build takes seconds and needs no downloads.

## Run

```sh
agentbox android start
agentbox android install build/remedy.apk
adb shell am start -n dev.agentbox.remedy/.MainActivity
```

The app logs to logcat with the tag `Remedy`, for example `Added medication: Amoxicillin`.
