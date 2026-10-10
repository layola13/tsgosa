function main(): i32 {
  const m = new Map<string, i32>();
  m.set("n", 42);
  const v = m.get("n");
  if (v !== undefined) { console.log(v * 2); }
  return 0;
}
