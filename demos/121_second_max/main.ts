function main(): i32 {
  const a: number[] = [5, 1, 9, 3, 9, 2];
  let m1: i32 = a[0];
  let m2: i32 = a[0];
  for (let i: i32 = 1; i < a.length; i++) {
    if (a[i] > m1) {
      m2 = m1;
      m1 = a[i];
    } else if (a[i] > m2) {
      m2 = a[i];
    }
  }
  console.log(m1, m2);
  return 0;
}