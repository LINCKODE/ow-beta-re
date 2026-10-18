#!/usr/bin/fish
env PROTON_LOG=1 WINEDEBUG=+timestamp,+pid,+tid,+seh,+threadname,+debugstr ./build-and-run.fish -- --BNetServer=127.0.0.1:1119 --lobbyServer=127.0.0.1:3724 --console --tank_Console
