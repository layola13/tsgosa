interface Inner { v: i32; }
interface Outer { id: i32; inner: Inner; }
function main(): i32 {
  const o: Outer = { id: 1, inner: { v: 42 } };
  console.log(o.id);
  console.log(o.inner.v);
  return 0;
}
