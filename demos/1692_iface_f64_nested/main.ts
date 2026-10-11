interface In { v: f64; }
interface Out { inner: In; n: i32; }
function main(): i32 {
  const o: Out = { inner: { v: 2.5 }, n: 3 };
  console.log(o.inner.v + o.n);
  return 0;
}
