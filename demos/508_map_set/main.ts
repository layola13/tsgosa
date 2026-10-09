function main(): i32 {
  const m = new Map<string, i32>();
  m.set("a", 1);
  m.set("b", 2);
  console.log(m.get("a") ?? -1);
  console.log(m.has("b") ? 1 : 0);
  console.log(m.size);
  m.delete("a");
  console.log(m.has("a") ? 1 : 0);
  const s = new Set<i32>();
  s.add(5);
  s.add(6);
  console.log(s.has(5) ? 1 : 0);
  console.log(s.size);
  s.delete(5);
  console.log(s.size);
  return 0;
}
