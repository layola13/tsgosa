function main(): i32 {
  const m = new Map<string, i32>();
  m.set("a", 1);
  m.set("b", 2);
  console.log(m.get("b") ?? -1);
  console.log(m.size);
  return 0;
}
