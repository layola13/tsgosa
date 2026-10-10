function main(): i32 {
  const s: string = "racecar";
  let ok = 1;
  for (let i = 0; i < 3; i = i + 1) { if (s[i] != s[6 - i]) { ok = 0; } }
  console.log(ok);
  return 0;
}
