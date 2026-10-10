function main(): i32 {
  const m = new Map<string, i32>();
  m.set("x", 3);
  console.log(m.get("x") ?? 0);
  m.set("x", 8);
  console.log(m.get("x") ?? 0);
  return 0;
}
