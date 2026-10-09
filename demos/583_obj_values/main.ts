interface K {
  a: i32;
  b: i32;
}
function main(): i32 {
  const o: K = { a: 3, b: 4 };
  const v = Object.values(o);
  console.log(v.length);
  console.log(v[0]);
  console.log(v[1]);
  return 0;
}
