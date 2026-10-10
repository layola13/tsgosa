function main(): i32 {
  const m = new Map<string, i32>();
  m.set("a", 1);
  m.set("b", 2);
  m.set("c", 3);
  console.log(m.size);
  let t = 0;
  m.forEach((v) => { t = t + v; });
  console.log(t);
  return 0;
}
