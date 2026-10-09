const s = "abcd";
for (const k in s) {
  if (k == 3) {
    break;
  }
  console.log(k);
}
