// The microphone, from pressing to talk until letting go: recorded as the
// browser likes to (Opus in WebM), then decoded into the 16 kHz mono Whisper
// hears, which an AudioContext at that rate resamples to on its own.
import { sampleRate } from './audio';

export type Recording = { stop: () => Promise<Float32Array>; cancel: () => void };

export async function record(): Promise<Recording> {
  const stream = await navigator.mediaDevices.getUserMedia({ audio: { channelCount: 1, echoCancellation: true, noiseSuppression: true } });
  const recorder = new MediaRecorder(stream);
  const parts: Blob[] = [];
  recorder.ondataavailable = (event) => event.data.size && parts.push(event.data);
  recorder.start();
  const release = () => stream.getTracks().forEach((track) => track.stop());
  return {
    stop: () =>
      new Promise((resolve, reject) => {
        recorder.onstop = async () => {
          release();
          try {
            const context = new AudioContext({ sampleRate });
            const decoded = await context.decodeAudioData(await new Blob(parts, { type: recorder.mimeType }).arrayBuffer());
            void context.close();
            resolve(decoded.getChannelData(0).slice());
          } catch (err) {
            reject(err instanceof Error ? err : new Error(String(err)));
          }
        };
        recorder.stop();
      }),
    cancel: () => {
      recorder.onstop = null;
      if (recorder.state !== 'inactive') recorder.stop();
      release();
    },
  };
}
