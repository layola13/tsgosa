interface P { a: i32; b: i32; }
function main(): i32 {
  const o: P = { a: 1, b: 2 };
  const v: i32[] = Object.values(o);
  console.log(v[1]);
  return 0;
}
