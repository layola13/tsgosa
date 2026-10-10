function main(): i32 {
  let c = 0;
  for (const ch of "abc") { if (ch == "b") { c = 1; } }
  console.log(c);
  return 0;
}
