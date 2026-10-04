function main(): i32 {
  const m = new Map();
  m.set("a", 1);
  m.set("b", 2);
  m.set("a", m.get("a") + 10);
  console.log(m.get("a"), m.get("b"), m.getSize());
  return 0;
}
