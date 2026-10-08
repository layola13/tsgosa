interface P {
  a: i32;
  b: i32;
}
function get(p: P, k: string): i32 {
  return p[k];
}
function main(): i32 {
  console.log(get({ a: 1, b: 2 }, "b"));
  console.log(get({ a: 1, b: 2 }, "zzz"));
  return 0;
}
