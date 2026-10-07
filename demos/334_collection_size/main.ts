function main(): i32 {
  const s = new Set<i32>();
  s.add(5);
  s.add(6);
  s.add(5);
  const m = new Map<string, i32>();
  m.set("a", 1);
  m.set("b", 2);
  return s.size * 10 + m.size;
}
console.log(main());
