function main(): i32 {
  const a: number[] = [1, 3, 5];
  const b: number[] = [2, 4, 6];
  const c: number[] = [];
  let i: i32 = 0;
  let j: i32 = 0;
  while (i < a.length && j < b.length) {
    if (a[i] < b[j]) {
      c.push(a[i]);
      i = i + 1;
    } else {
      c.push(b[j]);
      j = j + 1;
    }
  }
  while (i < a.length) {
    c.push(a[i]);
    i = i + 1;
  }
  while (j < b.length) {
    c.push(b[j]);
    j = j + 1;
  }
  console.log(c.length, c[0], c[5]);
  return 0;
}
