interface P {
  a: i32;
  b: i32;
}
function get(o: P, k: keyof P): i32 {
  return o[k];
}
function main(): i32 {
  const o: P = { a: 3, b: 4 };
  console.log(get(o, "b"));
  return 0;
}
