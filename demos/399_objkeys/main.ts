interface P {
  a: i32;
  b: i32;
}
function main(): i32 {
  const o: P = { ["a"]: 1, "b": 2 };
  console.log(o.a + o.b);
  return 0;
}
