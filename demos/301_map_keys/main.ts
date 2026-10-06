function main(): i32 {
  const m = new Map();
  m.set(1, 10);
  m.set(2, 20);
  let s: i32 = 0;
  for (const k of m.keys()) { s = s + k; }
  for (const v of m.values()) { s = s + v; }
  console.log(s);
  return s;
}
console.log(main());
