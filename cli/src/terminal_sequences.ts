const kittyKeyboardOffSequence = [
  '\x1b[=0u', // kitty keyboard protocol: reset enhancement flags
  '\x1b[<999u', // kitty keyboard protocol: pop enhancement stack
  '\x1b[=0u', // enforce flags=0 in case pop restores nonzero state
].join('');

const terminalResetSequence = [
  '\x1b[!p', // soft reset (DECSTR)
  kittyKeyboardOffSequence, // kitty keyboard protocol: force enhancements off
  '\x1b[>4;0m', // xterm modifyOtherKeys off (CSI u)
  '\x1b[?2004l', // bracketed paste off
  '\x1b[?2026l', // synchronized output off (DEC mode 2026)
  '\x1b[?1l', // application cursor keys off
  '\x1b[?1000l', // mouse tracking off
  '\x1b[?1002l',
  '\x1b[?1003l',
  '\x1b[?1006l',
  '\x1b[?25h', // show cursor
  '\x1b[0m', // reset SGR
].join('');

const terminalHardResetSequence = `\x1bc${terminalResetSequence}${kittyKeyboardOffSequence}`;

export { kittyKeyboardOffSequence, terminalResetSequence, terminalHardResetSequence };
