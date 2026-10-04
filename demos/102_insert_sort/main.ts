function main(): i32 {
  const a: number[] = [5, 2, 4, 1, 3];
  for (let i: i32 = 1; i < a.length; i++) {
    const k: i32 = a[i];
    let j: i32 = i - 1;
    while (j >= 0 && a[j] > k) {
      a[j + 1] = a[j];
      j = j - 1;
    }
    a[j + 1] = k;
  }
  console.log(a[0], a[1], a[2], a[3], a[4]);
  return 0;
}