function main(): i32 {
  const m = new Map<string, number>();
  m.set("a", 1); m.set("b", 2);
  expect(m.get("a")).toBe(1);
  const s = new Set<number>();
  s.add(5); s.add(6);
  expect(s.has(5)).toBe(1);
  console.log(m.get("b") + s.size);
  return 0;
}
