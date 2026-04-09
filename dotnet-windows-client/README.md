# dotnet-windows-client

Small WinForms client for the Strisper TCP transcription proxy.

## What it does

- Connects to a Strisper server such as `localhost:43007`
- Records 16 kHz mono microphone audio
- Sends audio over TCP and types confirmed transcript text into the active Windows application
- Lets you configure global start/stop hotkeys
- Persists server address, hotkeys, and an optional API-key field per user

## Notes

- The current proxy expects its upstream API key to be configured on the server side.
- The client stores the API-key field locally for convenience, but it does not inject that key into the TCP protocol.
- Stopping recording now half-closes the send side first so the client can still receive the final transcript lines.

## Build

From the repository root:

```bash
bash scripts/_25-build-dotnet-windows-client.sh
```
