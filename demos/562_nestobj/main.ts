interface Inner { b: i32; }
interface Outer { a: Inner; }
function main(): i32 {
  const o: Outer = { a: { b: 5 } };
  console.log(o.a.b);
  return 0;
}
