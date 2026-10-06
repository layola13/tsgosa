function main(): i32 {
  const a: f64 = parseFloat("4.5");
  const b: f64 = Number("42");
  const c: i32 = Number(5);
  let s = 0;
  if (a > 4) { s = s + 1; }
  if (b > 41) { s = s + 1; }
  if (c === 5) { s = s + 1; }
  console.log(s);
  return s;
}
console.log(main());
