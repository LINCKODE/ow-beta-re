-- Runs when the gateway's Lua state is created or reset (POST /lua/reset).
-- Put reusable helpers here; experiments go through POST /eval.

-- ids(): the newest session's JamIDs as raw 16-byte strings.
function ids(session)
  for _, s in ipairs(sessions()) do
    if session == nil or s.id == session then
      return unhex(s.client_jamid), unhex(s.server_jamid)
    end
  end
end

-- dump(n): print the last n messages, one per line.
function dump(n)
  for _, e in ipairs(recent(n or 20)) do
    print(string.format("%5d %s %-3s %-34s %5d %s", e.seq, e.time:sub(12, 19), e.dir, e.protocol, e.msg, e.payload))
  end
end

-- chat(channel_type, sender_name, sender_id, text): ClientInLobbyChat 20400 =
-- [channel {i32 type, u64, u64}] [sender {cstr name, u64, u64}] [cstr text].
-- An empty name or sender id 0 shows as a system message; the sender id must
-- differ from our own (20500) or the client drops it as an echo. The client
-- strips "#1234" from the name. Channel types 0-4 render in different colours.
function chat(ctype, name, sid, text)
  send(nil, 20400, pack("i q q", ctype, 0, 0) .. pack("z q q", name, sid, sid) .. pack("z", text))
end

-- command(line [, session]): run a lobbylogic "!" chat command as if the player
-- typed it (ClientOutLobbyChat 21700 = {i32 channel type, 16-byte channel id, cstring}).
-- The reply appears in the client's chat. Example: command("instance") sends the
-- client into a game on the instance server (Temple of Anubis; "instance 0x08..." for another map).
function command(line, session)
  inject(21700, pack("i q q z", 0, 0, 0, "!" .. line), session)
end
