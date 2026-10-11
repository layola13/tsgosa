interface Inner { b: i32; }
interface Outer { a: Inner; }
function main(): i32 {
  const o: Outer = { a: { b: 7 } };
  const { a: { b } } = o;
  console.log(b);
  return 0;
}
