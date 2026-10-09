const s = "hi";
let n = 2;
while ((s ?? "d") && n) {
  console.log(n);
  n = n - 1;
}
