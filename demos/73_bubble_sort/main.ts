function main(): i32 {
  const a: number[] = [3, 1, 4, 1, 5];
  for (let i: i32 = 0; i < 5; i++) {
    for (let j: i32 = 0; j < 4 - i; j++) {
      if (a[j] > a[j + 1]) {
        const t: i32 = a[j];
        a[j] = a[j + 1];
        a[j + 1] = t;
      }
    }
  }
  console.log(a[0] + a[4]);
  console.log(a[2]);
  return 0;
}
