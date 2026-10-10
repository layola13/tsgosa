function main(): i32 {
  const m = new Map<string, i32>();
  m.set("a", 1);
  m.set("b", 2);
  console.log(m.get("a")!);
  console.log(m.has("b") ? 1 : 0);
  console.log(m.size);
  return 0;
}
