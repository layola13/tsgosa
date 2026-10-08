interface Inner {
  v: i32;
}
interface Outer {
  inner: Inner;
  n: i32;
}
function main(): i32 {
  const o: Outer = JSON.parse("{\"inner\":{\"v\":40},\"n\":2}");
  console.log(o.inner.v + o.n);
  return 0;
}
