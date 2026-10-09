function main(): i32 {
  const m = new Map<string, i32>();
  m.set("a", 5);
  m.set("b", 7);
  let s = 0;
  m.forEach((v) => {
    s += v;
  });
  console.log(s);
  m.forEach((v, k) => {
    console.log(v);
    console.log(k.length);
  });
  return 0;
}
