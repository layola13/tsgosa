function main(): i32 {
  const m = new Map<string, i32>();
  m.set("a", 1);
  m.set("b", 2);
  console.log(m.has("c") ? 1 : 0);
  console.log(m.get("c") ?? 9);
  return 0;
}
