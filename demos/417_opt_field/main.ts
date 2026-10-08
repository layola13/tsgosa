interface P {
  a: i32;
  b?: i32;
}
function main(): i32 {
  const o: P = { a: 1 };
  console.log(o.a);
  return 0;
}
