interface P {
  a: i32;
}
function main(): i32 {
  const o: P = { a: 1 };
  console.log(Object.hasOwn(o, "a") ? 1 : 0);
  console.log(Number.isNaN(5) ? 1 : 0);
  return 0;
}
