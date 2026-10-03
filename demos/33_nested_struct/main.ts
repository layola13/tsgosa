interface Inner {
  v: i32;
}
interface Outer {
  name: string;
  inner: Inner;
}
function main(): i32 {
  const o: Outer = { name: "o", inner: { v: 40 } };
  console.log(o.inner.v + 2);
  return 0;
}
