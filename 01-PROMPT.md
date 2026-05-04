# Background
This repo (whisper_streaming, /work) provides a streaming speech to text proxy. Currently we have an Emacs client, and a Microsoft Windows client. (see README.md).

## End goal
I would like to create a Linux client that is compatible with Wayland (let's call the client `strisper-wayland`, I've created an empty subdir for it: `wayland-client`, analogous to our `emacs-client` and `dotnet-windows-client`). It should preferably integrate well with GNOME: let's have (configurable) global shortcut key which toggles transcription, let's make the default keybinding 'C-S-<F9>'.

## Reference
I've temporarily added repository under the path `/work/TalkType`, that's a separate "competing" product (unrelated to our current streaming implementation). We can use that implementation as inspiration for how to implement the Linux/Gnome/Wayland specific parts of our `stripser-wayland` package. get this working on Wayland and GNOME in particular. However, I do not want to rely on Python for the implementation of the program. I would rather see some statically typed language, perhaps Rust? or another language of your recommendation. own. Let's also avoid using  AppImage for our package.


## Task
Create a 02-IMPL-PLAN-WAYLAND-CLIENT.md file with detailed instructions that a junior developer can follow to fully implement our Wayland client. Be specific, give a rationale for why certain edits need to be made (if any), what files to create, what the files should contain, and how they are interrelated.
