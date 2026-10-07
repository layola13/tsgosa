function main(): i32 {
  const x: f64 = 1.5;
  const y = -x;
  const z = 1 + -x;
  let s: i32 = 0;
  if (y < 0) { s = s + 1; }
  if (z < 1) { s = s + 10; }
  console.log(s);
  return s;
}
console.log(main());
