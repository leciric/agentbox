// Keeps the main process alive when its stdout or stderr goes away. The app is
// often started by a terminal or a launcher that stops reading them, or is gone
// by the time the app logs: once a pipe is full, console writes queue up, and
// when its reader closes they fail with EPIPE. Node's console only ignores the
// errors of the write it's making, so a queued write's error reaches a stream
// with no 'error' listener and becomes an uncaught exception, which Electron
// shows as a "JavaScript error occurred in the main process" dialog. Electron
// logs every rejected ipcMain.handle with console.error, so a burst of failed
// calls, like a VM stopping, was enough.
//
// There's nowhere to report an error writing to stdio, so every one is ignored.
export function guardStdio(streams: NodeJS.WritableStream[] = [process.stdout, process.stderr]): void {
  for (const stream of streams) stream.on('error', () => {});
}
