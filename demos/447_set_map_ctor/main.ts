function main(): i32 {
  const s = new Set();
  s.add(1);
  s.add(2);
  const m = new Map();
  m.set("k", 7);
  console.log(s.getSize() + m.getSize());
  return 0;
}
