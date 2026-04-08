# Emacs Client

`strisper.el` is a small Emacs client for the TCP transcription proxy.

## `use-package` example

Add something like this to your `init.el`:

```elisp
(use-package strisper
  :ensure nil
  :load-path "~/src/whisper_streaming/emacs-client"
  :commands (strisper-record strisper-record-at-point strisper-stop)
  :bind (("C-c s r" . strisper-record)
         ("C-c s i" . strisper-record-at-point)
         ("C-c s s" . strisper-stop))
  :custom
  (strisper-record-command
   "arecord -f S16_LE -c1 -r 16000 -t raw -D pulse | nc -4 localhost 43007"))
```

Adjust `:load-path` to wherever this repository lives on your machine.

## Commands

- `M-x strisper-record`: start recording and keep transcript output in the internal process buffers
- `M-x strisper-record-at-point`: start recording and insert recognized text at point
- `M-x strisper-stop`: stop the active recording process

## Notes

- The default command expects `arecord` and `nc` to be installed.
- The proxy must already be running before you start recording.
- If Emacs runs inside Docker or Podman, you may need `host.docker.internal` instead of `localhost`.
