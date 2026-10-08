function main(): i32 {
  const m = new Map<string, i32>();
  m.set("a", 1);
  m.set("b", 2);
  console.log(m.get("a") + m.size);
  const a: i32[] = [1, 2, 3];
  console.log(a.reduce((s, x) => s + x, 0));
  return 0;
}
