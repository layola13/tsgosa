function main(): i32 {
  const t = Boolean(1);
  const f = Boolean(0);
  if (t) {
    console.log(1);
  }
  if (f) {
    console.log(2);
  }
  const z = 0;
  const n = 5;
  if (Boolean(z)) {
    console.log(4);
  }
  if (Boolean(n)) {
    console.log(5);
  }
  console.log(3);
  return 0;
}
