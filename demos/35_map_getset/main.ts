function main(): i32 {
  const m = new Map();
  m.set("a", 1);
  m.set("b", 2);
  console.log(m.get("a") + m.get("b"));
  console.log(m.has("c"));
  return 0;
}
